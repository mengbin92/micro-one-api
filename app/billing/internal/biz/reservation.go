package biz

import "time"

// Reservation lifecycle states. The committing/releasing intermediate states
// are only visible inside an in-flight transaction; outside of the transaction
// callers should only ever observe reserved/committed/released/expired.
const (
	ReservationStatusReserved   = "reserved"
	ReservationStatusCommitting = "committing"
	ReservationStatusCommitted  = "committed"
	ReservationStatusReleasing  = "releasing"
	ReservationStatusReleased   = "released"
	ReservationStatusExpired    = "expired"
)

// Reservation carries the dual-track (subscription + balance) pre-deduction for
// a single request. The reservation is the unit of idempotency for the entire
// commit/release cycle: its state machine guards the side effects on the wallet
// and the subscription usage counters, so the new "subscription priority"
// deduction flow is safe to retry.
type Reservation struct {
	ReservationID string
	UserID        string
	RequestID     string
	// Amount is the original fixed-point amount estimate. For the dual-track
	// flow it equals the total estimated request cost and is kept populated so legacy readers
	// continue to work; the field is not authoritative for the subscription
	// side of the pre-deduction.
	Amount                int64
	Status                string
	Model                 string
	ChannelID             string
	SubscriptionAccountID string

	// Subscription side of the pre-deduction. SubscriptionID==0 means the
	// reservation was created via the legacy balance-only path. The reservation
	// stores the original (un-multiplied) USD cost; the subscription limit
	// check is responsible for converting that to the accounting USD it
	// compares against limits.
	SubscriptionID        int64
	SubscriptionAmountUSD float64
	// Per-window reservation snapshot so the absorber check only credits the
	// reservation against the window that was active at pre-deduction time.
	// After the window rolls the snapshot mismatches and the pre-deduction no
	// longer consumes capacity from the new window.
	SubscriptionDailyWindowStart   int64
	SubscriptionWeeklyWindowStart  int64
	SubscriptionMonthlyWindowStart int64

	// Balance side of the pre-deduction. BalanceAmount is the
	// authoritative "frozen" amount that will be released / settled at commit
	// or release time. For the legacy balance-only path it equals Amount.
	BalanceAmount int64

	// ActualCost is the real settlement cost persisted at commit time
	// (code-review 2026-07-30 billing-L1). The reservation's Amount is only
	// a pre-deduction estimate; relaying it back on an idempotent retry of
	// CommitQuota misreports the committed amount whenever actual != estimate.
	// ActualCost is zero until the reservation reaches the committed state,
	// so a not-yet-committed reservation still reports via Amount.
	ActualCost int64

	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiredAt time.Time
}

func (r *Reservation) IsExpired() bool {
	return r.ExpiredAt.Before(time.Now())
}

// IsReserved reports whether the reservation is in the pre-deduction state and
// eligible for commit/release.
func (r *Reservation) IsReserved() bool {
	return r.Status == ReservationStatusReserved
}

// IsTerminal reports whether the reservation has reached a final state and
// must not be acted on again.
func (r *Reservation) IsTerminal() bool {
	if r == nil {
		return false
	}
	switch r.Status {
	case ReservationStatusCommitted, ReservationStatusReleased, ReservationStatusExpired:
		return true
	}
	return false
}

// HasSubscription reports whether this reservation carries a subscription
// pre-deduction. The "subscription priority" flow sets SubscriptionID != 0
// even when the cost was fully absorbed by the subscription (balance=0).
func (r *Reservation) HasSubscription() bool {
	return r != nil && r.SubscriptionID > 0
}
