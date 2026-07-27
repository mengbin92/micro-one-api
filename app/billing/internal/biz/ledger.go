package biz

import "time"

const (
	LedgerTypeConsume      = "consume"
	LedgerTypeRecharge     = "recharge"
	LedgerTypeRefund       = "refund"
	LedgerTypeRedeem       = "redeem"
	LedgerTypeSubscription = "subscription"
)

// CostSource identifies which cost dimension a ledger entry records. The
// "mixed" sentinel is reserved for future use; today the commit pipeline always
// emits one subscription-side entry and one balance-side entry so the values
// stay separated for reconciliation.
const (
	CostSourceSubscription = "subscription"
	CostSourceBalance      = "balance"
	CostSourceMixed        = "mixed"
)

// Ledger is the append-only audit trail for every wallet/subscription
// movement. The dual-track reservation flow records SubscriptionCost /
// BalanceCost so a single reservation can be reconciled against both
// dimensions; legacy entries leave both fields zero.
type Ledger struct {
	ID                    uint
	UserID                string
	Amount                int64
	UpstreamCost          int64
	BalanceAfter          int64
	Type                  string
	ReferenceID           string
	Remark                string
	TokenName             string
	ModelName             string
	Quota                 int64
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	ChannelID             int64
	SubscriptionAccountID int64
	ElapsedTime           int64
	IsStream              bool
	Endpoint              string
	// CostSource mirrors the CostSource constant and powers cost-dimension
	// aggregations.
	CostSource string
	// SubscriptionCost is the quota-equivalent share absorbed by the user's
	// subscription. Set together with BalanceCost=0 for a pure subscription
	// entry. Always paired with LedgerDedupeKey=ReferenceID:":type:subscription".
	SubscriptionCost int64
	// BalanceCost is the quota-equivalent share paid out of the user's wallet.
	// Always paired with LedgerDedupeKey=ReferenceID:":type:balance".
	BalanceCost int64
	// LedgerDedupeKey is the unique idempotency key for the entry. The format
	// is "{reservation_id}:{type}:{cost_source}" so retries from the CAS
	// pipeline never produce duplicate rows. Legacy paths (recharge, refund)
	// fall back to ReferenceID for compatibility.
	LedgerDedupeKey string
	Username        string // resolved from users table at read time
	CreatedAt       time.Time
}

// DailyAggregate holds per-day aggregated ledger stats (consume only).
type DailyAggregate struct {
	Date             string
	Quota            int64 // SUM(ABS(amount))
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	Count                 int64
	ElapsedTime      int64
}

// ModelAggregate holds per-model aggregated token stats (consume only).
type ModelAggregate struct {
	Model  string
	Tokens int64
}

// Usage aggregation dimensions for AggregateUsage.
const (
	UsageDimUser                = "user"
	UsageDimChannel             = "channel"
	UsageDimModel               = "model"
	UsageDimToken               = "token"
	UsageDimType                = "type"
	UsageDimDay                 = "day"
	UsageDimHour                = "hour"
	UsageDimSubscriptionAccount = "subscription_account"
)

// UsageFilter scopes a multi-dimensional usage aggregation query.
type UsageFilter struct {
	GroupBy               []string // ordered dimensions (see UsageDim* constants)
	UserID                string   // optional
	ChannelID             int64    // optional, 0 = any
	SubscriptionAccountID int64    // optional, 0 = any
	Model                 string   // optional
	Type                  string   // ledger type, defaults to consume when empty
	StartTime             time.Time
	EndTime               time.Time
	Limit                 int // optional top-N by quota desc, 0 = unlimited
}

// UsageBucket is one aggregated row keyed by the requested dimensions.
type UsageBucket struct {
	UserID                string
	ChannelID             int64
	SubscriptionAccountID int64
	Model                 string
	TokenName             string
	Type                  string
	Day                   string
	Hour                  string
	Quota                 int64
	UpstreamCost          int64
	GrossProfit           int64
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	Count                 int64
	ElapsedTime           int64
}

// UsageTotals holds grand totals across all buckets.
type UsageTotals struct {
	Quota            int64
	UpstreamCost     int64
	GrossProfit      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	Count                 int64
	ElapsedTime           int64
}

// AccountReceivable is a mirror of a single wallet overdraft event. It is
// written transactionally together with the negative-balance commit so the
// two stay in lockstep. The reservation_id is the unique idempotency key:
// one reservation can produce at most one receivable.
type AccountReceivable struct {
	ID            uint
	UserID        string
	ReservationID string
	// OverdueQuota is the *incremental* amount the user's wallet went more
	// negative by during this commit. Older overdraft is already recorded
	// against earlier receivables; we only persist the delta.
	OverdueQuota int64
	OverdueUSD   float64
	// Status mirrors the reservation's status: "pending" while the wallet is
	// still negative, "settled" once a recharge has matched the reservation
	// via the TopUpQuota flow. The settled transition is best-effort and is
	// not required for downstream correctness — the wallet balance is the
	// source of truth.
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	SettledAt    *time.Time
	SettledQuota int64
	Remark       string
}

const (
	ReceivableStatusPending = "pending"
	ReceivableStatusSettled = "settled"
)
