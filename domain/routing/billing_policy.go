package routing

import "math"

const (
	WalletOnly        = "wallet_only"
	SubscriptionOnly  = "subscription_only"
	SubscriptionFirst = "subscription_first"
)

type BillingPolicy struct {
	GroupID     int64
	Version     int64
	BillingMode string
	PriceRatio  float64
	EffectiveAt int64
}

func ValidBillingMode(mode string) bool {
	return mode == WalletOnly || mode == SubscriptionOnly || mode == SubscriptionFirst
}
func (p BillingPolicy) Valid() bool {
	return p.GroupID > 0 && ValidBillingMode(p.BillingMode) && p.PriceRatio > 0 && !math.IsNaN(p.PriceRatio) && !math.IsInf(p.PriceRatio, 0)
}
