package biz

import "time"

const (
	LedgerTypeConsume  = "consume"
	LedgerTypeRecharge = "recharge"
	LedgerTypeRefund   = "refund"
	LedgerTypeRedeem   = "redeem"
)

type Ledger struct {
	ID               uint
	UserID           string
	Amount           int64
	UpstreamCost     int64
	BalanceAfter     int64
	Type             string
	ReferenceID      string
	Remark           string
	TokenName        string
	ModelName        string
	Quota            int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	ChannelID           int64
	SubscriptionAccountID int64
	ElapsedTime          int64
	IsStream             bool
	Endpoint             string
	CreatedAt            time.Time
}

// DailyAggregate holds per-day aggregated ledger stats (consume only).
type DailyAggregate struct {
	Date             string
	Quota            int64 // SUM(ABS(amount))
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	Count            int64
	ElapsedTime      int64
}

// ModelAggregate holds per-model aggregated token stats (consume only).
type ModelAggregate struct {
	Model  string
	Tokens int64
}

// Usage aggregation dimensions for AggregateUsage.
const (
	UsageDimUser               = "user"
	UsageDimChannel            = "channel"
	UsageDimModel              = "model"
	UsageDimToken              = "token"
	UsageDimType               = "type"
	UsageDimDay                = "day"
	UsageDimHour               = "hour"
	UsageDimSubscriptionAccount = "subscription_account"
)

// UsageFilter scopes a multi-dimensional usage aggregation query.
type UsageFilter struct {
	GroupBy              []string // ordered dimensions (see UsageDim* constants)
	UserID               string   // optional
	ChannelID            int64    // optional, 0 = any
	SubscriptionAccountID int64   // optional, 0 = any
	Model                string   // optional
	Type                 string   // ledger type, defaults to consume when empty
	StartTime            time.Time
	EndTime              time.Time
	Limit                int // optional top-N by quota desc, 0 = unlimited
}

// UsageBucket is one aggregated row keyed by the requested dimensions.
type UsageBucket struct {
	UserID               string
	ChannelID            int64
	SubscriptionAccountID int64
	Model                string
	TokenName            string
	Type                 string
	Day                  string
	Hour                 string
	Quota                int64
	UpstreamCost         int64
	GrossProfit          int64
	PromptTokens         int64
	CompletionTokens     int64
	CacheReadTokens      int64
	Count                int64
	ElapsedTime          int64
}

// UsageTotals holds grand totals across all buckets.
type UsageTotals struct {
	Quota            int64
	UpstreamCost     int64
	GrossProfit      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	Count            int64
	ElapsedTime      int64
}
