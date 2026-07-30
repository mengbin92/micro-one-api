package data

import (
	"time"
)

type accountModel struct {
	ID           int64  `gorm:"primaryKey;column:id"`
	Username     string `gorm:"column:username"`
	DisplayName  string `gorm:"column:display_name"`
	Group        string `gorm:"column:group"`
	Balance      int64  `gorm:"column:balance"`
	UsedAmount   int64  `gorm:"column:used_amount"`
	RequestCount int64  `gorm:"column:request_count"`
	FrozenAmount int64  `gorm:"column:frozen_amount"`
	Status       int32  `gorm:"column:status"`
}

func (accountModel) TableName() string { return "users" }

type reservationModel struct {
	ID                    uint    `gorm:"primaryKey;column:id"`
	ReservationID         string  `gorm:"uniqueIndex;column:reservation_id"`
	UserID                string  `gorm:"index;column:user_id"`
	RequestID             string  `gorm:"index;column:request_id"`
	Amount                int64   `gorm:"column:amount"`
	Status                string  `gorm:"column:status"`
	Model                 *string `gorm:"column:model"`
	ChannelID             *string `gorm:"column:channel_id"`
	SubscriptionAccountID *string `gorm:"column:subscription_account_id"`

	// Subscription-side pre-deduction. SubscriptionID==0 means the reservation
	// was created via the legacy balance-only path. SubscriptionAmountUSD is
	// the original (un-multiplied) USD cost that will be settled against the
	// subscription's accounting USD window. The three window-start snapshots
	// are what the absorber check uses to decide whether the reservation
	// still consumes the current window's quota.
	SubscriptionID                 int64   `gorm:"column:subscription_id"`
	SubscriptionAmountUSD          float64 `gorm:"column:subscription_amount_usd"`
	SubscriptionDailyWindowStart   int64   `gorm:"column:subscription_daily_window_start"`
	SubscriptionWeeklyWindowStart  int64   `gorm:"column:subscription_weekly_window_start"`
	SubscriptionMonthlyWindowStart int64   `gorm:"column:subscription_monthly_window_start"`

	// Balance-side pre-deduction. Authoritative for the wallet side of the
	// dual-track flow; equals Amount on the legacy balance-only path.
	BalanceAmountQuota int64 `gorm:"column:balance_amount_quota"`

	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at"`
	ExpiredAt *time.Time `gorm:"index;column:expired_at"`
}

func (reservationModel) TableName() string { return "billing_reservations" }

type ledgerModel struct {
	ID                    uint    `gorm:"primaryKey;column:id"`
	UserID                string  `gorm:"index;column:user_id"`
	Amount                int64   `gorm:"column:amount"`
	UpstreamCost          int64   `gorm:"column:upstream_cost"`
	BalanceAfter          int64   `gorm:"column:balance_after"`
	Type                  string  `gorm:"index;column:type"`
	ReferenceID           *string `gorm:"index;column:reference_id"`
	Remark                *string `gorm:"column:remark"`
	TokenName             string  `gorm:"column:token_name"`
	ModelName             string  `gorm:"column:model_name;index"`
	Quota                 int64   `gorm:"column:quota"`
	PromptTokens          int64   `gorm:"column:prompt_tokens"`
	CompletionTokens      int64   `gorm:"column:completion_tokens"`
	CacheReadTokens       int64   `gorm:"column:cache_read_tokens"`
	CacheCreation5mTokens int64   `gorm:"column:cache_creation_5m_tokens"`
	CacheCreation1hTokens int64   `gorm:"column:cache_creation_1h_tokens"`
	// Per-bucket costs persisted for audit and vendor-invoice reconciliation
	// (v0.11.0 Phase 2 sub2api #9).
	PromptCost            int64  `gorm:"column:prompt_cost"`
	CompletionCost        int64  `gorm:"column:completion_cost"`
	CacheReadCost         int64  `gorm:"column:cache_read_cost"`
	CacheCreation5mCost   int64  `gorm:"column:cache_creation_5m_cost"`
	CacheCreation1hCost   int64  `gorm:"column:cache_creation_1h_cost"`
	ShadowCost            int64  `gorm:"column:shadow_cost"`
	ChannelID             int64  `gorm:"column:channel_id"`
	SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
	ElapsedTime           int64  `gorm:"column:elapsed_time"`
	IsStream              bool   `gorm:"column:is_stream"`
	Endpoint              string `gorm:"column:endpoint"`
	// Cost dimension tracking for the dual-track reservation flow. The dedupe
	// key is the unique idempotency anchor for the commit pipeline and is
	// independent of the legacy reference_id lookup.
	CostSource       string    `gorm:"column:cost_source"`
	SubscriptionCost int64     `gorm:"column:subscription_cost"`
	BalanceCost      int64     `gorm:"column:balance_cost"`
	LedgerDedupeKey  string    `gorm:"uniqueIndex:idx_ledger_dedupe_key;column:ledger_dedupe_key"`
	CreatedAt        time.Time `gorm:"index;column:created_at"`
	// Username is not a real column — it is scanned from the users table
	// via a LEFT JOIN in list/get queries. It must be tagged with `-:migration`
	// so GORM AutoMigrate does not try to add it to billing_ledgers.
	Username string `gorm:"->;-:migration"`
}

func (ledgerModel) TableName() string { return "billing_ledgers" }

// accountReceivableModel is the GORM mirror of account_receivables, which is
// the append-only ledger of wallet overdraft events produced by the
// dual-track commit pipeline.
type accountReceivableModel struct {
	ID            uint       `gorm:"primaryKey;column:id"`
	UserID        string     `gorm:"index:idx_account_receivable_user;column:user_id"`
	ReservationID string     `gorm:"uniqueIndex:idx_account_receivable_reservation;column:reservation_id"`
	OverdueQuota  int64      `gorm:"column:overdue_quota"`
	OverdueUSD    float64    `gorm:"column:overdue_usd"`
	Status        string     `gorm:"column:status"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at"`
	SettledAt     *time.Time `gorm:"column:settled_at"`
	SettledQuota  int64      `gorm:"column:settled_quota"`
	Remark        *string    `gorm:"column:remark"`
}

func (accountReceivableModel) TableName() string { return "account_receivables" }

type redeemCodeModel struct {
	ID        uint      `gorm:"primaryKey;column:id"`
	Code      string    `gorm:"uniqueIndex;column:code"`
	Name      *string   `gorm:"column:name"`
	Amount    int64     `gorm:"column:amount"`
	Count     int       `gorm:"column:count"`
	Status    int8      `gorm:"column:status"`
	CreatedBy *string   `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (redeemCodeModel) TableName() string { return "billing_redeem_codes" }

type redeemRecordModel struct {
	ID            uint      `gorm:"primaryKey;column:id"`
	UserID        string    `gorm:"index;column:user_id"`
	Code          string    `gorm:"index;column:code"`
	Amount        int64     `gorm:"column:amount"`
	BalanceBefore int64     `gorm:"column:balance_before"`
	BalanceAfter  int64     `gorm:"column:balance_after"`
	CreatedAt     time.Time `gorm:"index;column:created_at"`
}

func (redeemRecordModel) TableName() string { return "billing_redeem_records" }
