package biz

import "time"

const (
	LedgerTypeConsume  = "consume"
	LedgerTypeRecharge = "recharge"
	LedgerTypeRefund   = "refund"
	LedgerTypeRedeem   = "redeem"
)

type Ledger struct {
	ID           uint
	UserID       string
	Amount       int64
	BalanceAfter int64
	Type         string
	ReferenceID  string
	Remark       string
	TokenName        string
	ModelName        string
	Quota            int64
	PromptTokens     int64
	CompletionTokens int64
	ChannelID        int64
	ElapsedTime      int64
	IsStream         bool
	Endpoint         string
	CreatedAt        time.Time
}

// DailyAggregate holds per-day aggregated ledger stats (consume only).
type DailyAggregate struct {
	Date             string
	Quota            int64 // SUM(ABS(amount))
	PromptTokens     int64
	CompletionTokens int64
	Count            int64
	ElapsedTime      int64
}

// ModelAggregate holds per-model aggregated token stats (consume only).
type ModelAggregate struct {
	Model  string
	Tokens int64
}
