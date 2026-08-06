package biz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// fakeRefundRepo models MarkOrderRefunded: first call runs the revert and
// flips status to refunded; a second call short-circuits (idempotent).
type fakeRefundRepo struct {
	mu    sync.Mutex
	order *PaymentOrder
	calls int
}

func (r *fakeRefundRepo) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	return order, nil
}
func (r *fakeRefundRepo) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	return nil, nil
}
func (r *fakeRefundRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	return nil, 0, nil
}
func (r *fakeRefundRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *fakeRefundRepo) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *fakeRefundRepo) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == PaymentOrderStatusRefunded {
		c := *r.order
		return &c, false, nil
	}
	if r.order.Status != PaymentOrderStatusPaid {
		return nil, false, fmt.Errorf("payment order status %q cannot be refunded", r.order.Status)
	}
	if revert != nil {
		if err := revert(r.order, nil); err != nil {
			return nil, false, err
		}
	}
	r.order.Status = PaymentOrderStatusRefunded
	c := *r.order
	return &c, true, nil
}

// stubAccountRepo for refund tests only needs UpdateBalance.
type stubAccountRepo struct {
	balanceCalls int
	lastDelta    int64
	lastType     string
	newBalance   int64
	err          error
}

func (s *stubAccountRepo) GetAccountSnapshot(ctx context.Context, userID string) (*Account, error) {
	return &Account{UserID: userID}, nil
}
func (s *stubAccountRepo) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*Account, error) {
	return nil, nil
}
func (s *stubAccountRepo) UpdateBalance(ctx context.Context, userID string, delta int64, operationType string) (int64, error) {
	s.balanceCalls++
	s.lastDelta = delta
	s.lastType = operationType
	return s.newBalance, s.err
}
func (s *stubAccountRepo) UpdateBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, delta int64, operationType string) (int64, error) {
	return s.UpdateBalance(ctx, userID, delta, operationType)
}
func (s *stubAccountRepo) UpdateUsage(ctx context.Context, userID string, usedAmountDelta, requestCountDelta int64) error {
	return nil
}
func (s *stubAccountRepo) UpdateUsageInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, usedAmountDelta, requestCountDelta int64) error {
	return nil
}
func (s *stubAccountRepo) UpdateFrozenAmount(ctx context.Context, userID string, delta int64) error {
	return nil
}
func (s *stubAccountRepo) ReserveBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, amount int64, allowOverdraft bool) (int64, int64, int64, error) {
	return 0, 0, 0, nil
}
func (s *stubAccountRepo) CommitBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, reserved, actual int64, allowOverdraft bool) (int64, int64, error) {
	return 0, 0, nil
}
func (s *stubAccountRepo) ReleaseBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, reserved int64) (int64, error) {
	return 0, nil
}

// recordingLedgerRepo records created ledger entries.
func (s *stubAccountRepo) IncrementBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, amount int64) (int64, error) {
	return 0, nil
}

type recordingLedgerRepo struct {
	mu        sync.Mutex
	created   []*Ledger
	createErr error
}

func (r *recordingLedgerRepo) CreateLedger(ctx context.Context, ledger *Ledger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	r.created = append(r.created, ledger)
	return nil
}
func (r *recordingLedgerRepo) CreateLedgerInTx(ctx context.Context, tx subscriptionbiz.Tx, ledger *Ledger) error {
	return r.CreateLedger(ctx, ledger)
}
func (r *recordingLedgerRepo) GetLedgerByID(ctx context.Context, id int64) (*Ledger, error) {
	return nil, ErrLedgerNotFound
}
func (r *recordingLedgerRepo) ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*Ledger, int64, error) {
	return nil, 0, nil
}
func (r *recordingLedgerRepo) ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return nil, 0, nil
}
func (r *recordingLedgerRepo) ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return nil, 0, nil
}
func (r *recordingLedgerRepo) ListLedgersBySubscriptionAccount(ctx context.Context, subscriptionAccountID int64, page, pageSize int32) ([]*Ledger, int64, error) {
	return nil, 0, nil
}
func (r *recordingLedgerRepo) AggregateLedgerByDate(ctx context.Context, userID string, ledgerType string, startTime, endTime time.Time) ([]*DailyAggregate, []*ModelAggregate, error) {
	return nil, nil, nil
}
func (r *recordingLedgerRepo) AggregateUsage(ctx context.Context, filter UsageFilter) ([]*UsageBucket, *UsageTotals, error) {
	return nil, nil, nil
}
func (r *recordingLedgerRepo) FindByDedupeKey(ctx context.Context, tx subscriptionbiz.Tx, key string) (*Ledger, error) {
	return nil, nil
}
func (r *recordingLedgerRepo) SumSubscriptionCostByReservation(ctx context.Context, reservationIDs []string) (int64, error) {
	return 0, nil
}

// stubSubscriptionReverter records revoke/shorten calls.
type stubSubscriptionReverter struct {
	revokeCalls  int
	shortenCalls int
	lastSubID    int64
	lastReason   string
	lastSubtract int64
	revokeErr    error
	shortenErr   error
}

func (s *stubSubscriptionReverter) Revoke(ctx context.Context, subscriptionID int64, reason string) error {
	s.revokeCalls++
	s.lastSubID = subscriptionID
	s.lastReason = reason
	return s.revokeErr
}
func (s *stubSubscriptionReverter) RevokeInTx(ctx context.Context, tx subscriptionbiz.Tx, subscriptionID int64, reason string) error {
	return s.Revoke(ctx, subscriptionID, reason)
}
func (s *stubSubscriptionReverter) Shorten(ctx context.Context, subscriptionID int64, subtractSeconds int64) error {
	s.shortenCalls++
	s.lastSubID = subscriptionID
	s.lastSubtract = subtractSeconds
	return s.shortenErr
}
func (s *stubSubscriptionReverter) ShortenInTx(ctx context.Context, tx subscriptionbiz.Tx, subscriptionID int64, subtractSeconds int64) error {
	return s.Shorten(ctx, subscriptionID, subtractSeconds)
}
func (s *stubSubscriptionReverter) GetActiveSubscriptionForUser(ctx context.Context, userID int64) (*subscriptionbiz.UserSubscription, error) {
	return &subscriptionbiz.UserSubscription{ID: 99, UserID: userID}, nil
}

func (s *stubSubscriptionReverter) GetActiveSubscriptionForUserInTx(ctx context.Context, tx subscriptionbiz.Tx, userID int64) (*subscriptionbiz.UserSubscription, error) {
	return s.GetActiveSubscriptionForUser(ctx, userID)
}

func newRefundOrder(tradeNo string, paid bool) *PaymentOrder {
	snap := PlanSnapshot{PlanID: 7, Name: "Pro", GroupID: 3, PriceQuota: 2000, ValidityDays: 30}
	status := PaymentOrderStatusPending
	if paid {
		status = PaymentOrderStatusPaid
	}
	return &PaymentOrder{
		UserID:           "42",
		TradeNo:          tradeNo,
		AssetType:        PaymentAssetTypeSubscription,
		AssetAmount:      30,
		MoneyCents:       200000,
		GroupID:          3,
		PlanID:           7,
		PlanSnapshot:     EncodePlanSnapshot(snap),
		Status:           status,
		AssetIssueStatus: PaymentAssetIssueStatusIssued,
	}
}

func TestRefund_RevokePolicyRevokesSubscription(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-1", true)}
	// Stash the subscription id in ProviderPayload so the reverter can find it.
	repo.order.ProviderPayload = `{"subscription_id":"5"}`
	accounts := &stubAccountRepo{newBalance: 5000}
	ledger := &recordingLedgerRepo{}
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, accounts, ledger, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{
		TradeNo:  "PAY-RF-1",
		Reason:   "user requested",
		Policy:   RefundPolicyRevoke,
		Operator: "admin",
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if res.RefundedQuota != 2000 {
		t.Fatalf("refunded quota = %d, want 2000", res.RefundedQuota)
	}
	if accounts.balanceCalls != 1 || accounts.lastDelta != 2000 || accounts.lastType != LedgerTypeRefund {
		t.Fatalf("wallet credit = %+v", accounts)
	}
	if len(ledger.created) != 1 {
		t.Fatalf("ledger entries = %d, want 1", len(ledger.created))
	}
	l := ledger.created[0]
	if l.Type != LedgerTypeRefund || l.CostSource != CostSourceReversal {
		t.Fatalf("ledger type/source = %s/%s", l.Type, l.CostSource)
	}
	wantKey := "PAY-RF-1:" + LedgerTypeRefund + ":" + CostSourceReversal
	if l.LedgerDedupeKey != wantKey {
		t.Fatalf("dedupe key = %q, want %q", l.LedgerDedupeKey, wantKey)
	}
	if reverter.revokeCalls != 1 || reverter.lastSubID != 5 {
		t.Fatalf("reverter = %+v", reverter)
	}
	if res.SubscriptionAct != "revoked" {
		t.Fatalf("subscription act = %q", res.SubscriptionAct)
	}
}

func TestRefund_RevokePolicyFallsBackToActiveSubscription(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-ACTIVE", true)}
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, &stubAccountRepo{newBalance: 5000}, &recordingLedgerRepo{}, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{
		TradeNo: "PAY-RF-ACTIVE",
		Policy:  RefundPolicyRevoke,
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if reverter.revokeCalls != 1 || reverter.lastSubID != 99 {
		t.Fatalf("reverter = %+v, want active subscription id 99", reverter)
	}
	if res.SubscriptionID != 99 {
		t.Fatalf("subscription_id = %d, want 99", res.SubscriptionID)
	}
}

func TestRefund_IdempotentAcrossReplays(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-2", true)}
	repo.order.ProviderPayload = `{"subscription_id":"6"}`
	accounts := &stubAccountRepo{newBalance: 5000}
	ledger := &recordingLedgerRepo{}
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, accounts, ledger, reverter)

	if _, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{TradeNo: "PAY-RF-2", Policy: RefundPolicyRevoke}); err != nil {
		t.Fatalf("first refund: %v", err)
	}
	firstBalanceCalls := accounts.balanceCalls
	firstLedger := len(ledger.created)
	firstRevoke := reverter.revokeCalls

	// Replay the refund callback.
	if _, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{TradeNo: "PAY-RF-2", Policy: RefundPolicyRevoke}); err != nil {
		t.Fatalf("replay refund: %v", err)
	}
	if accounts.balanceCalls != firstBalanceCalls {
		t.Fatalf("wallet credited again on replay: %d -> %d", firstBalanceCalls, accounts.balanceCalls)
	}
	if len(ledger.created) != firstLedger {
		t.Fatalf("ledger written again on replay: %d -> %d", firstLedger, len(ledger.created))
	}
	if reverter.revokeCalls != firstRevoke {
		t.Fatalf("subscription revoked again on replay: %d -> %d", firstRevoke, reverter.revokeCalls)
	}
}

func TestRefund_RejectsNonPaidOrder(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-3", false)} // pending
	uc := NewRefundUsecase(repo, &stubAccountRepo{}, &recordingLedgerRepo{}, &stubSubscriptionReverter{})
	_, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{TradeNo: "PAY-RF-3", Policy: RefundPolicyRevoke})
	if err == nil {
		t.Fatal("expected error refunding a pending order")
	}
}

func TestRefund_ShortenPolicyShortensSubscription(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-4", true)}
	repo.order.ProviderPayload = `{"subscription_id":"8"}`
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, &stubAccountRepo{newBalance: 5000}, &recordingLedgerRepo{}, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{TradeNo: "PAY-RF-4", Policy: RefundPolicyShorten})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if reverter.shortenCalls != 1 || reverter.lastSubID != 8 {
		t.Fatalf("reverter = %+v", reverter)
	}
	// 30 days * seconds/day
	if reverter.lastSubtract != 30*subscriptionSecondsPerDay {
		t.Fatalf("subtract = %d, want %d", reverter.lastSubtract, 30*subscriptionSecondsPerDay)
	}
	if res.SubscriptionAct != "shortened" {
		t.Fatalf("act = %q", res.SubscriptionAct)
	}
}

func TestRefund_KeepPolicyLeavesSubscriptionUntouched(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-5", true)}
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, &stubAccountRepo{newBalance: 5000}, &recordingLedgerRepo{}, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{TradeNo: "PAY-RF-5", Policy: RefundPolicyKeep})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if reverter.revokeCalls != 0 || reverter.shortenCalls != 0 {
		t.Fatalf("keep policy mutated subscription: %+v", reverter)
	}
	if res.SubscriptionAct != "kept" {
		t.Fatalf("act = %q", res.SubscriptionAct)
	}
}

// Compile-time guards to avoid unused-import churn if tests are trimmed.
var _ = errors.New
var _ = subscriptionbiz.SubscriptionStatusActive

// TestRefund_PrefersSubscriptionIDColumnOverActiveFallback verifies the phase
// 2.3 traceability fix: when the order carries a subscription_id column (written
// by the assigner at MarkOrderPaid), the refund revokes THAT subscription
// deterministically, even though the user's current active subscription is a
// different row. Without the column the refund would fall back to the active
// subscription and revoke the wrong one.
func TestRefund_PrefersSubscriptionIDColumnOverActiveFallback(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-TRACE", true)}
	// The order granted subscription 5 (stamped on the column at MarkOrderPaid).
	repo.order.SubscriptionID = 5
	reverter := &stubSubscriptionReverter{}
	// GetActiveSubscriptionForUser returns a DIFFERENT active subscription (99),
	// which is the wrong target if the fallback path were taken.
	uc := NewRefundUsecase(repo, &stubAccountRepo{newBalance: 5000}, &recordingLedgerRepo{}, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{
		TradeNo: "PAY-RF-TRACE",
		Policy:  RefundPolicyRevoke,
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if reverter.revokeCalls != 1 || reverter.lastSubID != 5 {
		t.Fatalf("refund revoked %d (calls=%d), want the column-stamped subscription 5 (not the active 99)",
			reverter.lastSubID, reverter.revokeCalls)
	}
	if res.SubscriptionID != 5 {
		t.Fatalf("result subscription_id = %d, want 5", res.SubscriptionID)
	}
}

// TestRefund_LegacyPayloadSubscriptionIDStillWorks verifies the backward-compat
// fallback path: orders fulfilled before the subscription_id column existed
// still resolve via ProviderPayload.subscription_id.
func TestRefund_LegacyPayloadSubscriptionIDStillWorks(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-LEGACY", true)}
	// No column; legacy payload carries the link.
	repo.order.SubscriptionID = 0
	repo.order.ProviderPayload = `{"subscription_id":"7"}`
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, &stubAccountRepo{newBalance: 5000}, &recordingLedgerRepo{}, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{
		TradeNo: "PAY-RF-LEGACY",
		Policy:  RefundPolicyRevoke,
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if reverter.lastSubID != 7 {
		t.Fatalf("legacy payload refund resolved subscription %d, want 7", reverter.lastSubID)
	}
	if res.SubscriptionID != 7 {
		t.Fatalf("result subscription_id = %d, want 7", res.SubscriptionID)
	}
}

// TestRefund_UsesActualPaidAmountNotPlanPrice (review §6 regression for H5):
// the refund wallet credit must equal the amount the user actually paid
// (MoneyCents/100), not the plan's nominal PriceQuota. Previously the code
// overwrote the paid amount with the plan snapshot's PriceQuota, causing
// over/under-refunds on discounted orders.
func TestRefund_UsesActualPaidAmountNotPlanPrice(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-PAID", true)}
	// Simulate a discounted order: the user paid 150000 cents (1500 quota)
	// for a plan whose nominal price is 2000 quota.
	repo.order.MoneyCents = 150000
	repo.order.ProviderPayload = `{"subscription_id":"11"}`
	accounts := &stubAccountRepo{newBalance: 5000}
	ledger := &recordingLedgerRepo{}
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, accounts, ledger, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{
		TradeNo: "PAY-RF-PAID",
		Policy:  RefundPolicyRevoke,
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	// Refunded quota must be the actual paid amount (1500), not the plan
	// nominal price (2000).
	if res.RefundedQuota != 1500 {
		t.Fatalf("refunded quota = %d, want 1500 (actual paid, not plan price 2000)", res.RefundedQuota)
	}
	if accounts.lastDelta != 1500 {
		t.Fatalf("wallet credit delta = %d, want 1500", accounts.lastDelta)
	}
	if len(ledger.created) != 1 || ledger.created[0].Amount != 1500 {
		t.Fatalf("ledger amount = %v, want 1500", ledger.created)
	}
}

// TestRefund_FallsBackToPlanPriceWhenMoneyCentsZero (review H5 legacy path):
// legacy orders without a populated money_cents column fall back to the plan
// snapshot's PriceQuota so the refund is not zero.
func TestRefund_FallsBackToPlanPriceWhenMoneyCentsZero(t *testing.T) {
	repo := &fakeRefundRepo{order: newRefundOrder("PAY-RF-LEGACY", true)}
	repo.order.MoneyCents = 0 // legacy: no money_cents
	repo.order.ProviderPayload = `{"subscription_id":"12"}`
	accounts := &stubAccountRepo{newBalance: 5000}
	ledger := &recordingLedgerRepo{}
	reverter := &stubSubscriptionReverter{}
	uc := NewRefundUsecase(repo, accounts, ledger, reverter)

	res, err := uc.RefundSubscriptionOrder(context.Background(), RefundRequest{
		TradeNo: "PAY-RF-LEGACY",
		Policy:  RefundPolicyRevoke,
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	// Plan snapshot PriceQuota is 2000; fallback must use it.
	if res.RefundedQuota != 2000 {
		t.Fatalf("refunded quota = %d, want 2000 (plan snapshot fallback)", res.RefundedQuota)
	}
}
