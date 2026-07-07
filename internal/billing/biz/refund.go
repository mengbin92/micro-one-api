package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	subscriptionbiz "micro-one-api/internal/subscription/biz"

	"gorm.io/gorm"
)

// Refund / reversal accounting semantics (phase 2.3).
//
// A refund reverses a completed subscription purchase. The accounting model is:
//
//   - The payment order transitions to status="refunded". This is a terminal
//     state alongside "paid" and "closed"; a refunded order cannot be re-paid
//     or re-closed.
//   - A reversal ledger entry of type=LedgerTypeRefund is written with a
//     dedicated cost_source=CostSourceReversal and a dedupe key scoped to the
//     trade_no so duplicate refund callbacks cannot double-refund.
//   - The wallet is credited back the purchase price (UpdateBalance +price).
//   - The subscription is shortened or revoked according to the refund policy:
//       * "revoke":   the subscription is revoked (status=revoked). Used when
//                     the refund happens before any meaningful consumption.
//       * "shorten":  the subscription's expires_at is pulled back by the
//                     refunded plan's validity. Used when the user already
//                     consumed part of the subscription and the refund is
//                     prorated against remaining time.
//       * "keep":     the subscription is left untouched. Used when the refund
//                     is a goodwill credit and the entitlement is not clawed
//                     back.
//
// Idempotency: the reversal ledger dedupe key is
//   "{trade_no}:refund:{cost_source}" (mirroring the consume dedupe key
// format). The DB unique constraint on ledger_dedupe_key guarantees at most
// one reversal row per trade_no, so a replayed refund callback is a no-op.

const (
	// PaymentOrderStatusRefunded marks an order whose purchase has been
	// reversed. It is terminal.
	PaymentOrderStatusRefunded = "refunded"

	// CostSourceReversal marks ledger entries that reverse a prior purchase.
	// It is distinct from CostSourceBalance so reconciliation can separate
	// consumption refunds (CostSourceBalance) from purchase reversals.
	CostSourceReversal = "reversal"
)

// RefundPolicy controls what happens to the subscription entitlement when a
// purchase is refunded.
type RefundPolicy string

const (
	RefundPolicyRevoke  RefundPolicy = "revoke"
	RefundPolicyShorten RefundPolicy = "shorten"
	RefundPolicyKeep    RefundPolicy = "keep"
)

// RefundRequest is the input to RefundSubscriptionOrder.
type RefundRequest struct {
	TradeNo  string
	Reason   string
	Policy   RefundPolicy
	Operator string
}

// RefundResult summarises the reversal.
type RefundResult struct {
	OrderID         int64
	TradeNo         string
	RefundedQuota   int64
	BalanceAfter    int64
	SubscriptionID  int64
	SubscriptionAct string // revoked / shortened / kept
	LedgerDedupeKey string
}

// RefundRepo is the narrow interface RefundSubscriptionOrder needs from the
// payment repository: a row-locked mark-refunded that runs the caller's
// reversal inside the same transaction so the order status, wallet credit,
// and ledger entry commit atomically.
type RefundRepo interface {
	// MarkOrderRefunded transitions a paid order to refunded inside a
	// transaction. The revert callback runs inside the tx and must perform
	// the wallet credit + ledger write + subscription mutation. Returns
	// changed=false when the order was already refunded (idempotent).
	MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, *gorm.DB) error) (*PaymentOrder, bool, error)
}

// SubscriptionReverter abstracts the subscription-side mutation a refund
// performs (revoke / shorten / keep). The billing domain owns the wallet and
// ledger; the subscription domain owns the entitlement row.
type SubscriptionReverter interface {
	Revoke(ctx context.Context, subscriptionID int64, reason string) error
	Shorten(ctx context.Context, subscriptionID int64, subtractSeconds int64) error
	RevokeInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, reason string) error
	ShortenInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, subtractSeconds int64) error
	GetActiveSubscriptionForUser(ctx context.Context, userID int64) (*subscriptionbiz.UserSubscription, error)
}

// refundUsecase coordinates the order status, wallet credit, ledger reversal,
// and subscription mutation for a refund.
type RefundUsecase struct {
	orders        RefundRepo
	accounts      AccountRepo
	ledger        LedgerRepo
	subscriptions SubscriptionReverter
	now           func() time.Time
}

// NewRefundUsecase wires the refund coordinator.
func NewRefundUsecase(orders RefundRepo, accounts AccountRepo, ledger LedgerRepo, subscriptions SubscriptionReverter) *RefundUsecase {
	return &RefundUsecase{
		orders:        orders,
		accounts:      accounts,
		ledger:        ledger,
		subscriptions: subscriptions,
		now:           time.Now,
	}
}

// RefundSubscriptionOrder reverses a completed subscription purchase. It is
// idempotent: a second call for an already-refunded order is a no-op.
func (uc *RefundUsecase) RefundSubscriptionOrder(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if uc == nil || uc.orders == nil {
		return nil, errors.New("refund usecase is not configured")
	}
	if req.TradeNo == "" {
		return nil, errors.New("trade_no is required")
	}
	policy := req.Policy
	if policy == "" {
		policy = RefundPolicyRevoke
	}
	var result *RefundResult
	order, changed, err := uc.orders.MarkOrderRefunded(ctx, req.TradeNo, req.Reason, func(order *PaymentOrder, tx *gorm.DB) error {
		if order.AssetType != PaymentAssetTypeSubscription {
			return fmt.Errorf("refund only supports subscription asset orders, got %q", order.AssetType)
		}
		// Decode the plan snapshot to recover the purchase price and validity
		// without re-reading the (possibly off-shelf) plan row.
		snap, snapErr := DecodePlanSnapshot(order.PlanSnapshot)
		if snapErr != nil {
			return fmt.Errorf("decode plan snapshot for refund: %w", snapErr)
		}
		refundQuota := order.MoneyCents / 100
		if snap.PriceQuota > 0 {
			refundQuota = snap.PriceQuota
		}
		// Credit the wallet back the purchase price in the same transaction that
		// transitions the order and mutates the subscription entitlement.
		newBalance, err := uc.accounts.UpdateBalanceInTx(ctx, tx, order.UserID, refundQuota, LedgerTypeRefund)
		if err != nil {
			return fmt.Errorf("refund wallet credit: %w", err)
		}
		// Write the reversal ledger entry. The dedupe key is scoped to the
		// trade_no so a replayed callback cannot double-refund.
		dedupeKey := fmt.Sprintf("%s:%s:%s", order.TradeNo, LedgerTypeRefund, CostSourceReversal)
		remark := req.Reason
		if remark == "" {
			remark = fmt.Sprintf("refund subscription order %s", order.TradeNo)
		}
		if req.Operator != "" {
			remark = fmt.Sprintf("%s (by %s)", remark, req.Operator)
		}
		ledger := &Ledger{
			UserID:          order.UserID,
			Amount:          refundQuota,
			BalanceAfter:    newBalance,
			Type:            LedgerTypeRefund,
			ReferenceID:     order.TradeNo,
			Remark:          remark,
			CostSource:      CostSourceReversal,
			LedgerDedupeKey: dedupeKey,
		}
		if err := uc.ledger.CreateLedgerInTx(ctx, tx, ledger); err != nil {
			return fmt.Errorf("create reversal ledger: %w", err)
		}
		// Mutate the subscription entitlement according to the policy.
		subID, subAct, subErr := uc.applySubscriptionReversal(ctx, tx, order, snap, policy, req.Reason)
		if subErr != nil {
			return subErr
		}
		result = &RefundResult{
			OrderID:         order.ID,
			TradeNo:         order.TradeNo,
			RefundedQuota:   refundQuota,
			BalanceAfter:    newBalance,
			SubscriptionID:  subID,
			SubscriptionAct: subAct,
			LedgerDedupeKey: dedupeKey,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !changed && result == nil {
		// Idempotent re-entry: order was already refunded. Reconstruct a
		// minimal result so the caller sees a stable response.
		return &RefundResult{
			OrderID: order.ID,
			TradeNo: order.TradeNo,
		}, nil
	}
	if result == nil {
		result = &RefundResult{OrderID: order.ID, TradeNo: order.TradeNo}
	}
	return result, nil
}

// applySubscriptionReversal performs the subscription-side mutation selected by
// the refund policy. It returns the subscription id and a human-readable action
// so the result can be audited.
func (uc *RefundUsecase) applySubscriptionReversal(ctx context.Context, tx *gorm.DB, order *PaymentOrder, snap PlanSnapshot, policy RefundPolicy, reason string) (int64, string, error) {
	if uc.subscriptions == nil {
		return 0, "skipped", nil
	}
	subID := uc.refundSubscriptionID(ctx, order)
	switch policy {
	case RefundPolicyKeep:
		return subID, "kept", nil
	case RefundPolicyShorten:
		if subID <= 0 {
			return 0, "", errors.New("shorten refund requires a subscription_id")
		}
		subtract := int64(snap.ValidityDays) * subscriptionSecondsPerDay
		if err := uc.subscriptions.ShortenInTx(ctx, tx, subID, subtract); err != nil {
			return 0, "", fmt.Errorf("shorten subscription: %w", err)
		}
		return subID, "shortened", nil
	case RefundPolicyRevoke:
		fallthrough
	default:
		if subID <= 0 {
			return 0, "", errors.New("revoke refund requires a subscription_id")
		}
		revokeReason := reason
		if revokeReason == "" {
			revokeReason = fmt.Sprintf("refund of order %s", order.TradeNo)
		}
		if err := uc.subscriptions.RevokeInTx(ctx, tx, subID, revokeReason); err != nil {
			return 0, "", fmt.Errorf("revoke subscription: %w", err)
		}
		return subID, "revoked", nil
	}
}

func (uc *RefundUsecase) refundSubscriptionID(ctx context.Context, order *PaymentOrder) int64 {
	if order == nil {
		return 0
	}
	if order.ProviderPayload != "" {
		var pm map[string]string
		if json.Unmarshal([]byte(order.ProviderPayload), &pm) == nil {
			if subID, err := strconv.ParseInt(pm["subscription_id"], 10, 64); err == nil && subID > 0 {
				return subID
			}
		}
	}
	userID, err := strconv.ParseInt(order.UserID, 10, 64)
	if err != nil || userID <= 0 || uc.subscriptions == nil {
		return 0
	}
	sub, err := uc.subscriptions.GetActiveSubscriptionForUser(ctx, userID)
	if err != nil || sub == nil {
		return 0
	}
	return sub.ID
}
