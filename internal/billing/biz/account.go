package biz

type Account struct {
	UserID       string
	Username     string
	DisplayName  string
	Group        string
	Quota        int64
	UsedQuota    int64
	RequestCount int64
	FrozenQuota  int64
	Status       int32
}

func (a *Account) AvailableQuota() int64 {
	return a.Quota - a.FrozenQuota
}

// GroupRatio returns the ratio for this account's group using default ratios.
// Prefer using BillingUsecase.getGroupRatio() which supports externalized config.
func (a *Account) GroupRatio() float64 {
	ratios := DefaultGroupRatios()
	if ratio, ok := ratios[a.Group]; ok {
		return ratio
	}
	return 1.0
}
