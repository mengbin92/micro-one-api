package subscriptiondto

import (
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

func ProgressToProto(p *biz.SubscriptionProgress) (*billingv1.SubscriptionUsage, error) {
	if p == nil {
		return nil, nil
	}
	var contract []byte
	if p.Contract != nil {
		var err error
		contract, err = jsonx.Marshal(p.Contract)
		if err != nil {
			return nil, err
		}
	}
	dimension := func(d *biz.QuotaDimension) *billingv1.SubscriptionUsageDimension {
		if d == nil {
			return nil
		}
		return &billingv1.SubscriptionUsageDimension{Used: d.Used, Limit: d.Limit, Remaining: d.Remaining, NextRefresh: d.NextRefresh, Settled: d.Settled, Frozen: d.Frozen, Available: d.Available, Unlimited: d.Unlimited, OverLimit: d.OverLimit, WindowStart: d.WindowStart}
	}
	return &billingv1.SubscriptionUsage{Id: p.ID, Status: string(p.Status), StartsAt: p.StartsAt, ExpiresAt: p.ExpiresAt, GroupId: p.GroupID, SubscriptionName: p.SubscriptionName, DailyUsed: dimension(p.DailyUsed), WeeklyUsed: dimension(p.WeeklyUsed), MonthlyUsed: dimension(p.MonthlyUsed), RemainingSeconds: p.RemainingSeconds, RateMultiplier: p.RateMultiplier, UsageSource: p.UsageSource, ObservedAt: p.ObservedAt, ContractJson: contract}, nil
}

func ProgressFromProto(p *billingv1.SubscriptionUsage) (*biz.SubscriptionProgress, error) {
	if p == nil {
		return nil, nil
	}
	var contract *biz.SubscriptionContract
	if len(p.ContractJson) != 0 {
		if err := jsonx.Unmarshal(p.ContractJson, &contract); err != nil {
			return nil, err
		}
	}
	dimension := func(d *billingv1.SubscriptionUsageDimension) *biz.QuotaDimension {
		if d == nil {
			return nil
		}
		return &biz.QuotaDimension{Used: d.Used, Limit: d.Limit, Remaining: d.Remaining, NextRefresh: d.NextRefresh, Settled: d.Settled, Frozen: d.Frozen, Available: d.Available, Unlimited: d.Unlimited, OverLimit: d.OverLimit, WindowStart: d.WindowStart}
	}
	return &biz.SubscriptionProgress{ID: p.Id, Status: biz.SubscriptionStatus(p.Status), StartsAt: p.StartsAt, ExpiresAt: p.ExpiresAt, GroupID: p.GroupId, SubscriptionName: p.SubscriptionName, DailyUsed: dimension(p.DailyUsed), WeeklyUsed: dimension(p.WeeklyUsed), MonthlyUsed: dimension(p.MonthlyUsed), RemainingSeconds: p.RemainingSeconds, RateMultiplier: p.RateMultiplier, UsageSource: p.UsageSource, ObservedAt: p.ObservedAt, Contract: contract}, nil
}
