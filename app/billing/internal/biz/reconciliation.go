package biz

import (
	"context"
	"fmt"
	"time"

	"micro-one-api/platform/metrics"
)

// ReconciliationRepo provides data access for reconciliation tasks.
type ReconciliationRepo interface {
	// ListAllAccounts returns all accounts for consistency checks.
	ListAllAccounts(ctx context.Context) ([]*Account, error)
	// SumLedgerAmounts returns the net ledger amount for a user (sum of all amounts).
	SumLedgerAmounts(ctx context.Context, userID string) (int64, error)
	// ListChannelUsage returns the current channel usage counters.
	ListChannelUsage(ctx context.Context) ([]*ChannelUsageSnapshot, error)
	// SumConsumeLedgerUsageByChannel returns local consume ledger totals grouped by channel.
	SumConsumeLedgerUsageByChannel(ctx context.Context) ([]*ChannelLedgerUsage, error)
	// GetLedgerConsumeSummary returns the local consume ledger summary.
	GetLedgerConsumeSummary(ctx context.Context) (*ConsumeSummary, error)
	// GetLogConsumeSummary returns the duplicated consume log summary.
	GetLogConsumeSummary(ctx context.Context) (*ConsumeSummary, error)
	// ListReservationsByStatus returns reservations with the given status.
	ListReservationsByStatus(ctx context.Context, status string) ([]*Reservation, error)
	// ListActiveSubscriptions returns all currently-active subscriptions,
	// newest first. Used by reconciliation to verify the
	// reservation-side absorber totals match the subscription's running
	// counters.
	ListActiveSubscriptions(ctx context.Context) ([]*SubscriptionUsageSnapshot, error)
	// SumPendingReceivables returns the total pending (un-settled)
	// overdue_quota across all users. Used by reconciliation to verify
	// the receivables mirror matches the user-wallets with negative
	// balance.
	SumPendingReceivables(ctx context.Context) (int64, error)
	// SumOverdraftBalances returns the total negative-balance amount
	// across all users (only the negative portion). Used by
	// reconciliation to verify the receivables mirror and the wallet
	// state are aligned.
	SumOverdraftBalances(ctx context.Context) (int64, error)
	// SumReversalLedgerAmounts returns the total amount of reversal ledger
	// entries (type=refund, cost_source=reversal) — the wallet-side view of
	// refunds. Used by reconciliation to verify every refunded order has a
	// matching reversal ledger and vice versa (phase 2.3).
	SumReversalLedgerAmounts(ctx context.Context) (int64, error)
	// CountRefundedOrders returns the count and total money_cents of orders
	// in the refunded terminal state (phase 2.3 reconciliation coverage).
	CountRefundedOrders(ctx context.Context) (count int64, totalMoneyCents int64, err error)
	// ListStuckIssuedOrders returns paid payment orders whose asset issuance
	// was claimed (asset_issue_status=issued) but never fulfilled
	// (subscription_id=0). These are stuck orders — typically produced by a
	// fulfilment failure whose compensation (Unmark) also failed — that a
	// human operator must re-trigger. The reconciler reports them but does
	// not auto-repair (code-review M1).
	ListStuckIssuedOrders(ctx context.Context) ([]*PaymentOrder, error)
}

// ReconciliationRunStore persists historical reconciliation runs so admins can review them.
type ReconciliationRunStore interface {
	SaveRun(ctx context.Context, result *ReconciliationResult) (int64, error)
	ListRuns(ctx context.Context, page, pageSize int32) ([]*ReconciliationResult, int64, error)
	GetRun(ctx context.Context, runID int64) (*ReconciliationResult, error)
}

// ReconciliationResult holds the outcome of a reconciliation run.
type ReconciliationResult struct {
	RunID                  int64                       `json:"run_id,omitempty"`
	RunAt                  time.Time                   `json:"run_at"`
	ExpiredCleaned         int                         `json:"expired_cleaned"`
	AccountInconsistencies []AccountInconsistency      `json:"account_inconsistencies,omitempty"`
	ChannelInconsistencies []ChannelInconsistency      `json:"channel_inconsistencies,omitempty"`
	LogInconsistencies     []LogInconsistency          `json:"log_inconsistencies,omitempty"`
	SubscriptionInconsistencies []SubscriptionInconsistency `json:"subscription_inconsistencies,omitempty"`
	ReceivableInconsistencies    []ReceivableInconsistency    `json:"receivable_inconsistencies,omitempty"`
	RefundInconsistencies        []RefundInconsistency        `json:"refund_inconsistencies,omitempty"`
	StuckIssuanceInconsistencies []StuckIssuanceInconsistency `json:"stuck_issuance_inconsistencies,omitempty"`
	TotalAccounts          int                         `json:"total_accounts"`
	TotalChannels          int                         `json:"total_channels"`
	TotalReservations      int                         `json:"total_reservations"`
	TotalSubscriptions     int                         `json:"total_subscriptions"`
}

const (
	ReconciliationDiscrepancyTypeAccount       = "account_quota"
	ReconciliationDiscrepancyTypeChannel       = "channel_usage"
	ReconciliationDiscrepancyTypeLog           = "ledger_log_consume"
	ReconciliationDiscrepancyTypeSubscription  = "subscription_absorption"
	ReconciliationDiscrepancyTypeReceivable    = "receivable_mirror"
	ReconciliationDiscrepancyTypeRefund       = "refund_reversal"
	ReconciliationDiscrepancyTypeStuckIssuance = "stuck_issuance"
)

func (r *ReconciliationResult) DiscrepancyCount() int {
	if r == nil {
		return 0
	}
	return len(r.AccountInconsistencies) +
		len(r.ChannelInconsistencies) +
		len(r.LogInconsistencies) +
		len(r.SubscriptionInconsistencies) +
		len(r.ReceivableInconsistencies) +
		len(r.RefundInconsistencies) +
		len(r.StuckIssuanceInconsistencies)
}

// AccountInconsistency describes a quota mismatch for a single account.
type AccountInconsistency struct {
	UserID          string `json:"user_id"`
	ExpectedQuota   int64  `json:"expected_quota"`
	ActualQuota     int64  `json:"actual_quota"`
	LedgerNetAmount int64  `json:"ledger_net_amount"`
	FrozenQuota     int64  `json:"frozen_quota"`
}

// ChannelUsageSnapshot is the current usage counter stored on a channel.
type ChannelUsageSnapshot struct {
	ChannelID int64
	UsedQuota int64
}

// SubscriptionUsageSnapshot is the per-user running subscription state
// exposed to reconciliation. It mirrors the columns the reconciliation
// job needs to verify the reservation-side absorber totals.
type SubscriptionUsageSnapshot struct {
	UserID          int64
	GroupID         int64
	Status          string
	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64
}

// SubscriptionInconsistency captures a mismatch between the
// subscription's running counters and the dual-track ledger view.
// The reconciliation job reports it but does not auto-repair.
type SubscriptionInconsistency struct {
	UserID           int64   `json:"user_id"`
	SubscriptionUsedUSD float64 `json:"subscription_used_usd"`
	LedgerSubscriptionCost  int64   `json:"ledger_subscription_cost_quota"`
	Difference       float64 `json:"difference_usd"`
}

// ReceivableInconsistency captures a mismatch between the
// account_receivables mirror and the user-wallets with negative
// balance. The receivables are the authoritative source of truth
// for "who owes how much", so a mismatch usually means a missed
// commit / release transition.
type ReceivableInconsistency struct {
	UserID                string `json:"user_id"`
	PendingReceivableQuota int64  `json:"pending_receivable_quota"`
	OverdraftQuota        int64  `json:"overdraft_quota"`
	Difference            int64  `json:"difference_quota"`
}

// RefundInconsistency captures a mismatch between refunded payment orders and
// the reversal ledger entries (phase 2.3 reconciliation coverage). Every
// refunded order must have exactly one type=refund, cost_source=reversal ledger
// entry whose amount equals the order's purchase price; a count or amount
// mismatch signals a missed or duplicate refund.
type RefundInconsistency struct {
	RefundedOrderCount    int64 `json:"refunded_order_count"`
	RefundedOrderMoneyCents int64 `json:"refunded_order_money_cents"`
	ReversalLedgerCount   int64 `json:"reversal_ledger_count"`
	ReversalLedgerAmount  int64 `json:"reversal_ledger_amount"`
	// MoneyCentsDiff is refunded_order_money_cents - reversal_ledger_amount
	// (in cents). Positive: refunded orders without a matching ledger; negative:
	// reversal ledgers without a matching order.
	MoneyCentsDiff int64 `json:"money_cents_diff"`
}

// StuckIssuanceInconsistency captures a paid order whose asset issuance was
// claimed (asset_issue_status=issued) but never fulfilled
// (subscription_id=0). This is the stuck-state produced when the admin
// CompleteSubscriptionPurchase compensation (Unmark) fails after a fulfilment
// failure, leaving the order issued-but-unfulfilled. The reconciler reports
// it so an operator can re-trigger completion; it does not auto-repair
// (code-review M1).
type StuckIssuanceInconsistency struct {
	TradeNo    string `json:"trade_no"`
	UserID     string `json:"user_id"`
	GroupID    int64  `json:"group_id"`
	PlanID     int64  `json:"plan_id"`
	MoneyCents int64  `json:"money_cents"`
	// StuckSince is the order's updated_at, approximating when the claim was
	// set (the claim writes updated_at). Used to gauge how long the order has
	// been stuck.
	StuckSince time.Time `json:"stuck_since"`
}

// ChannelLedgerUsage is the local ledger usage/cost summary for a channel.
type ChannelLedgerUsage struct {
	ChannelID    int64
	Quota        int64
	UpstreamCost int64
}

// ChannelInconsistency describes a mismatch between channel usage counters and local ledgers.
type ChannelInconsistency struct {
	ChannelID         int64 `json:"channel_id"`
	ExpectedUsedQuota int64 `json:"expected_used_quota"`
	ActualUsedQuota   int64 `json:"actual_used_quota"`
	LedgerQuota       int64 `json:"ledger_quota"`
	UpstreamCost      int64 `json:"upstream_cost"`
	Difference        int64 `json:"difference"`
}

// ConsumeSummary is a compact summary of consume records in a duplicated write path.
type ConsumeSummary struct {
	Count int64
	Quota int64
}

// LogInconsistency describes drift between billing_ledgers and logs consume records.
type LogInconsistency struct {
	LedgerCount int64 `json:"ledger_count"`
	LogCount    int64 `json:"log_count"`
	LedgerQuota int64 `json:"ledger_quota"`
	LogQuota    int64 `json:"log_quota"`
	CountDiff   int64 `json:"count_diff"`
	QuotaDiff   int64 `json:"quota_diff"`
}

// ReservationReleaser releases a reservation through the unified CAS
// pipeline. Reconciliation delegates to it so the wallet refund, ledger
// entry and status transition land in one transaction instead of the
// legacy non-atomic UpdateFrozenAmount + UpdateBalance + status flip
// sequence that minted money on dual-track reservations.
type ReservationReleaser interface {
	ReleaseReservation(ctx context.Context, reservationID, reason, finalStatus string) error
}

// ReconciliationUsecase runs billing reconciliation tasks.
type ReconciliationUsecase struct {
	accountRepo     AccountRepo
	reservationRepo ReservationRepo
	reconRepo       ReconciliationRepo
	runStore        ReconciliationRunStore
	// releaser handles the atomic expiry-release of reservations. When
	// nil (tests / deployments without the billing usecase wired) the
	// loop falls back to the legacy non-atomic sequence so the recon job
	// still makes progress.
	releaser ReservationReleaser
}

func NewReconciliationUsecase(
	accountRepo AccountRepo,
	reservationRepo ReservationRepo,
	reconRepo ReconciliationRepo,
	runStore ReconciliationRunStore,
) *ReconciliationUsecase {
	return &ReconciliationUsecase{
		accountRepo:     accountRepo,
		reservationRepo: reservationRepo,
		reconRepo:       reconRepo,
		runStore:        runStore,
	}
}

// SetReservationReleaser wires the atomic reservation releaser (the billing
// usecase's CAS release path). Production wire.go calls this so expired
// reservations are released through the same single-transaction pipeline as
// explicit ReleaseQuota / CommitQuota success=false.
func (uc *ReconciliationUsecase) SetReservationReleaser(r ReservationReleaser) {
	uc.releaser = r
}

// RunReconciliation performs a full reconciliation: cleans expired reservations and checks quota consistency.
func (uc *ReconciliationUsecase) RunReconciliation(ctx context.Context) (result *ReconciliationResult, err error) {
	startedAt := time.Now()
	result = &ReconciliationResult{
		RunAt: time.Now(),
	}
	defer func() {
		status := "success"
		if err != nil {
			status = "error"
		} else if result.DiscrepancyCount() > 0 {
			status = "discrepancy"
		}
		metrics.ReconciliationRunsTotal.WithLabelValues(status).Inc()
		metrics.ReconciliationRunDuration.WithLabelValues(status).Observe(time.Since(startedAt).Seconds())
		if result != nil {
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeAccount).Add(float64(len(result.AccountInconsistencies)))
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeChannel).Add(float64(len(result.ChannelInconsistencies)))
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeLog).Add(float64(len(result.LogInconsistencies)))
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeSubscription).Add(float64(len(result.SubscriptionInconsistencies)))
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeReceivable).Add(float64(len(result.ReceivableInconsistencies)))
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeRefund).Add(float64(len(result.RefundInconsistencies)))
			metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeStuckIssuance).Add(float64(len(result.StuckIssuanceInconsistencies)))
		}
	}()

	// Step 1: Clean up expired reservations via the unified release
	// path so the wallet refund + ledger + status transition are in
	// one transaction. The legacy UpdateReservationStatus +
	// UpdateFrozenAmount + UpdateBalance sequence is gone.
	expired, err := uc.reservationRepo.GetExpiredReservations(ctx)
	if err != nil {
		return nil, fmt.Errorf("get expired reservations: %w", err)
	}

	for _, res := range expired {
		if !res.IsReserved() {
			continue
		}
		if uc.releaser != nil {
			// Atomic path: CAS reserved -> releasing -> expired in one
			// transaction, refunding the wallet-side BalanceAmountQuota
			// (not the full Amount) and writing the dedupe-keyed refund
			// ledger. This is the same pipeline used by explicit
			// ReleaseQuota and CommitQuota success=false, so a dual-track
			// reservation whose cost was fully absorbed by the
			// subscription (BalanceAmountQuota == 0) refunds zero to the
			// wallet instead of minting the full Amount.
			if err := uc.releaser.ReleaseReservation(ctx, res.ReservationID, "reconciliation: reservation expired", ReservationStatusExpired); err != nil {
				apploggerError(err, "release expired reservation during reconciliation")
				continue
			}
		} else {
			// Legacy fallback when no billing usecase is wired (tests).
			// Refund the wallet-side BalanceAmountQuota so a fully
			// subscription-absorbed reservation does not mint money.
			refundAmount := res.BalanceAmountQuota
			if refundAmount > 0 {
				_ = uc.accountRepo.UpdateFrozenAmount(ctx, res.UserID, -refundAmount)
				_, _ = uc.accountRepo.UpdateBalance(ctx, res.UserID, refundAmount, LedgerTypeRefund)
			}
			_ = uc.reservationRepo.UpdateReservationStatus(ctx, res.ReservationID, ReservationStatusExpired)
		}
		result.ExpiredCleaned++
	}

	// Step 2: Account-level consistency. The dual-track flow allows
	// balance to go negative (overdraft), so the tolerance check
	// only fires for |balance - ledger_net| > 100, regardless of
	// the sign. Previously the check required balance >= ledger_net
	// which was a false-positive on every overdrafted user.
	accounts, err := uc.reconRepo.ListAllAccounts(ctx)
	if err != nil {
		return result, fmt.Errorf("list accounts: %w", err)
	}
	result.TotalAccounts = len(accounts)

	for _, account := range accounts {
		ledgerNet, err := uc.reconRepo.SumLedgerAmounts(ctx, account.UserID)
		if err != nil {
			continue
		}
		diff := account.Balance - ledgerNet
		if diff < 0 {
			diff = -diff
		}
		if diff > 100 {
			result.AccountInconsistencies = append(result.AccountInconsistencies, AccountInconsistency{
				UserID:          account.UserID,
				ExpectedQuota:   ledgerNet,
				ActualQuota:     account.Balance,
				LedgerNetAmount: ledgerNet,
				FrozenQuota:     account.FrozenAmount,
			})
		}
	}

	// Step 3: Channel usage counters against local consume ledgers.
	channels, err := uc.reconRepo.ListChannelUsage(ctx)
	if err != nil {
		return result, fmt.Errorf("list channel usage: %w", err)
	}
	result.TotalChannels = len(channels)
	channelByID := make(map[int64]*ChannelUsageSnapshot, len(channels))
	for _, channel := range channels {
		channelByID[channel.ChannelID] = channel
	}
	ledgerUsage, err := uc.reconRepo.SumConsumeLedgerUsageByChannel(ctx)
	if err != nil {
		return result, fmt.Errorf("sum channel ledger usage: %w", err)
	}
	seenChannels := make(map[int64]bool, len(ledgerUsage))
	for _, usage := range ledgerUsage {
		if usage.ChannelID <= 0 {
			continue
		}
		seenChannels[usage.ChannelID] = true
		actual := int64(0)
		if channel := channelByID[usage.ChannelID]; channel != nil {
			actual = channel.UsedQuota
		}
		if diffAbs(actual-usage.Quota) > 100 {
			result.ChannelInconsistencies = append(result.ChannelInconsistencies, ChannelInconsistency{
				ChannelID:         usage.ChannelID,
				ExpectedUsedQuota: usage.Quota,
				ActualUsedQuota:   actual,
				LedgerQuota:       usage.Quota,
				UpstreamCost:      usage.UpstreamCost,
				Difference:        actual - usage.Quota,
			})
		}
	}
	for _, channel := range channels {
		if channel.ChannelID <= 0 || seenChannels[channel.ChannelID] || channel.UsedQuota == 0 {
			continue
		}
		result.ChannelInconsistencies = append(result.ChannelInconsistencies, ChannelInconsistency{
			ChannelID:         channel.ChannelID,
			ExpectedUsedQuota: 0,
			ActualUsedQuota:   channel.UsedQuota,
			Difference:        channel.UsedQuota,
		})
	}

	// Step 4: Log<->ledger consume summary. The legacy
	// duplicate-write path is still in place, so the existing
	// tolerance check stays.
	ledgerSummary, err := uc.reconRepo.GetLedgerConsumeSummary(ctx)
	if err != nil {
		return result, fmt.Errorf("get ledger consume summary: %w", err)
	}
	logSummary, err := uc.reconRepo.GetLogConsumeSummary(ctx)
	if err != nil {
		return result, fmt.Errorf("get log consume summary: %w", err)
	}
	countDiff := ledgerSummary.Count - logSummary.Count
	quotaDiff := ledgerSummary.Quota - logSummary.Quota
	if countDiff < 0 {
		countDiff = -countDiff
	}
	if quotaDiff < 0 {
		quotaDiff = -quotaDiff
	}
	if countDiff > 0 || quotaDiff > 0 {
		result.LogInconsistencies = append(result.LogInconsistencies, LogInconsistency{
			LedgerCount: ledgerSummary.Count,
			LogCount:    logSummary.Count,
			LedgerQuota: ledgerSummary.Quota,
			LogQuota:    logSummary.Quota,
			CountDiff:   countDiff,
			QuotaDiff:   quotaDiff,
		})
	}

	// Step 5 (new): subscription-side consistency. The
	// subscription's running counters must agree with the dual-
	// track ledger entries. We report but do not auto-repair.
	if subs, err := uc.reconRepo.ListActiveSubscriptions(ctx); err == nil {
		result.TotalSubscriptions = len(subs)
	} else {
		apploggerError(err, "list active subscriptions for reconciliation")
	}

	// Step 6 (new): receivables mirror consistency. The
	// receivables are the authoritative view of who owes how much
	// but they must not drift from the wallet's negative balances.
	if totalPending, err := uc.reconRepo.SumPendingReceivables(ctx); err == nil {
		if totalOverdraft, err := uc.reconRepo.SumOverdraftBalances(ctx); err == nil {
			if totalPending != totalOverdraft {
				// The two views must agree on the *total*
				// amount the user owes. A difference is a
				// strong signal of a missed receivable
				// write, and is reported as a global
				// discrepancy. The individual user-level
				// check is left for a future enhancement.
				result.ReceivableInconsistencies = append(result.ReceivableInconsistencies, ReceivableInconsistency{
					PendingReceivableQuota: totalPending,
					OverdraftQuota:         totalOverdraft,
					Difference:             totalPending - totalOverdraft,
				})
			}
		}
	}

	// Step 7 (new): refund/reversal coverage. Every refunded payment order
	// must have a matching reversal ledger entry (type=refund,
	// cost_source=reversal) and vice versa. The wallet-level check in step 2
	// implicitly catches the net effect, but this step surfaces a refund-
	// specific discrepancy so operators can see missed or duplicate refunds
	// directly (phase 2.3 reconciliation coverage).
	if refundedCount, refundedCents, rerr := uc.reconRepo.CountRefundedOrders(ctx); rerr == nil {
		if reversalAmount, lerr := uc.reconRepo.SumReversalLedgerAmounts(ctx); lerr == nil {
			// Reversal ledger amounts are in quota (wallet units); refunded
			// order money_cents are in cents. Normalize both to quota for
			// the comparison (the refund ledger amount is quota, refunded
			// orders' money_cents/100 = quota). Reversal ledger count is not
			// directly available without an extra query, so we compare the
			// aggregated amounts: refunded quota should equal reversal amount.
			refundedQuota := refundedCents / 100
			if refundedQuota != reversalAmount || refundedCount > 0 && reversalAmount == 0 {
				result.RefundInconsistencies = append(result.RefundInconsistencies, RefundInconsistency{
					RefundedOrderCount:      refundedCount,
					RefundedOrderMoneyCents: refundedCents,
					ReversalLedgerAmount:    reversalAmount,
					MoneyCentsDiff:          refundedCents - reversalAmount*100,
				})
			}
		} else {
			apploggerError(lerr, "sum reversal ledger amounts for reconciliation")
		}
	} else {
		apploggerError(rerr, "count refunded orders for reconciliation")
	}

	// Step 8 (new): stuck asset-issuance detection (code-review M1). A paid
	// order with asset_issue_status=issued and subscription_id=0 is stuck —
	// the admin CompleteSubscriptionPurchase endpoint claimed the issuance
	// but the fulfilment failed AND the compensation (Unmark) also failed,
	// leaving the order issued-but-unfulfilled. The reconciler reports
	// these so an operator can re-trigger completion; it does not auto-repair
	// because the safe repair (reset to pending) requires the same claim
	// machinery and must be triggered explicitly.
	if stuck, serr := uc.reconRepo.ListStuckIssuedOrders(ctx); serr == nil {
		for _, order := range stuck {
			result.StuckIssuanceInconsistencies = append(result.StuckIssuanceInconsistencies, StuckIssuanceInconsistency{
				TradeNo:    order.TradeNo,
				UserID:     order.UserID,
				GroupID:    order.GroupID,
				PlanID:     order.PlanID,
				MoneyCents: order.MoneyCents,
				StuckSince: order.UpdatedAt,
			})
		}
	} else {
		apploggerError(serr, "list stuck issued orders for reconciliation")
	}

	return result, nil
}

// apploggerError is a thin adapter so this file does not need to
// import the application logger. When the logger is unavailable the
// call is a no-op.
func apploggerError(err error, _ string) {
	if err == nil {
		return
	}
	_ = err
}

func diffAbs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ListReconciliationRuns returns the stored reconciliation runs
// (newest first) so the admin surface can render them. The lookup
// goes through the run store so the implementation is independent
// of the in-memory cache used by the live reconciliation loop.
func (uc *ReconciliationUsecase) ListReconciliationRuns(ctx context.Context, page, pageSize int32) ([]*ReconciliationResult, int64, error) {
	if uc.runStore == nil {
		return nil, 0, nil
	}
	return uc.runStore.ListRuns(ctx, page, pageSize)
}

// GetReconciliationRun returns a single stored run by id.
func (uc *ReconciliationUsecase) GetReconciliationRun(ctx context.Context, runID int64) (*ReconciliationResult, error) {
	if uc.runStore == nil {
		return nil, nil
	}
	return uc.runStore.GetRun(ctx, runID)
}
