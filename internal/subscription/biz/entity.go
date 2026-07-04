package biz

type SubscriptionStatus string

const (
	SubscriptionStatusActive  SubscriptionStatus = "active"
	SubscriptionStatusExpired SubscriptionStatus = "expired"
	SubscriptionStatusRevoked SubscriptionStatus = "revoked"
)

const (
	SubscriptionGroupStatusEnabled  int32 = 1
	SubscriptionGroupStatusDisabled int32 = 0
)

type UserSubscription struct {
	ID               int64              `json:"id"`
	UserID           int64              `json:"user_id"`
	GroupID          int64              `json:"group_id"`
	SubscriptionName string             `json:"subscription_name"`
	Status           SubscriptionStatus `json:"status"`
	StartsAt         int64              `json:"starts_at"`
	ExpiresAt        int64              `json:"expires_at"`

	DailyUsageUSD   float64 `json:"daily_usage_usd"`
	WeeklyUsageUSD  float64 `json:"weekly_usage_usd"`
	MonthlyUsageUSD float64 `json:"monthly_usage_usd"`

	DailyWindowStart   int64 `json:"daily_window_start"`
	WeeklyWindowStart  int64 `json:"weekly_window_start"`
	MonthlyWindowStart int64 `json:"monthly_window_start"`

	Metadata  string `json:"metadata"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type SubscriptionGroup struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Platform         string `json:"platform"`
	SubscriptionType string `json:"subscription_type"`

	DailyLimitUSD   *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`
	RateMultiplier  float64  `json:"rate_multiplier"`
	Status          int32    `json:"status"`
	// PriceQuota stores the configured self-purchase price amount. The JSON/DB
	// name is kept for compatibility with earlier quota-based pricing.
	PriceQuota   int64 `json:"price_quota"`
	DurationDays int32 `json:"duration_days"`
	CreatedAt    int64 `json:"created_at"`
	UpdatedAt    int64 `json:"updated_at"`
}

type SubscriptionPlan struct {
	ID            int64              `json:"id"`
	GroupID       int64              `json:"group_id"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	PriceQuota    int64              `json:"price_quota"`
	OriginalPrice *int64             `json:"original_price,omitempty"`
	ValidityDays  int32              `json:"validity_days"`
	ValidityUnit  string             `json:"validity_unit"`
	Features      string             `json:"features"`
	ProductName   string             `json:"product_name"`
	ForSale       bool               `json:"for_sale"`
	SortOrder     int32              `json:"sort_order"`
	CreatedAt     int64              `json:"created_at"`
	UpdatedAt     int64              `json:"updated_at"`
	Group         *SubscriptionGroup `json:"group,omitempty"`
}

type QuotaDimension struct {
	Used      float64  `json:"used"`
	Limit     *float64 `json:"limit"`
	Remaining float64  `json:"remaining"`
}

type QuotaCheckResult struct {
	Allowed bool            `json:"allowed"`
	Reasons []string        `json:"reasons"`
	Daily   *QuotaDimension `json:"daily"`
	Weekly  *QuotaDimension `json:"weekly"`
	Monthly *QuotaDimension `json:"monthly"`
}

type SubscriptionProgress struct {
	ID               int64              `json:"id"`
	Status           SubscriptionStatus `json:"status"`
	StartsAt         int64              `json:"starts_at"`
	ExpiresAt        int64              `json:"expires_at"`
	DailyUsed        *QuotaDimension    `json:"daily_used"`
	WeeklyUsed       *QuotaDimension    `json:"weekly_used"`
	MonthlyUsed      *QuotaDimension    `json:"monthly_used"`
	RemainingSeconds int64              `json:"remaining_seconds"`
}

type AssignSubscriptionRequest struct {
	UserID           int64  `json:"user_id"`
	GroupID          int64  `json:"group_id"`
	SubscriptionName string `json:"subscription_name"`
	StartsAt         int64  `json:"starts_at"`
	ExpiresAt        int64  `json:"expires_at"`
	Metadata         string `json:"metadata"`
}
