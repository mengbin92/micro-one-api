package biz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

type RoutingGroupPrice struct {
	GroupKey, Version, Source, BillingMode string
	PriceRatio                             float64
	SubscriptionCovered                    bool
	// UserPrice is set when a user-specific override is the effective ratio.
	UserPrice *UserPriceOverride
}

// RoutingGroupPrice exposes the same effective multiplier as reserve. This is
// a quote at read time; the request snapshot remains the settlement authority.
func (uc *BillingUsecase) RoutingGroupPrice(ctx context.Context, groupID, userID int64) (*RoutingGroupPrice, error) {
	if !RequestSnapshotsEnabled() || uc.routingGroups == nil || groupID <= 0 {
		return nil, ErrRequestSnapshotUnavailable
	}
	g, err := uc.routingGroups.GetRoutingGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil || g.Status == "archived" {
		return nil, ErrRoutingContextInvalid
	}
	pricingUC := *uc
	if uc.pricingStore != nil {
		config, err := uc.pricingStore.GetPricingConfig(ctx)
		if err != nil {
			return nil, ErrRequestSnapshotUnavailable
		}
		pricingUC.pricingStore = capturedPricingStore{config}
	}
	config := pricingUC.pricingConfig(ctx)
	ratio := uc.getGroupRatio(config, g.Key)
	source := "billing_default"
	if _, ok := config.GroupRatios[g.Key]; ok {
		source = "GroupRatio"
	}
	raw, _ := jsonx.Marshal(struct {
		Group  string
		Ratio  float64
		Source string
	}{g.Key, ratio, source})
	p := &RoutingGroupPrice{GroupKey: g.Key, PriceRatio: ratio, Version: fmt.Sprintf("legacy_projection:%x", sha256.Sum256(raw)), Source: source, BillingMode: "subscription_first"}
	if uc.routingPolicies != nil {
		policy, err := uc.routingPolicies.Get(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if policy != nil {
			p.PriceRatio = policy.PriceRatio
			p.Version = fmt.Sprintf("routing_policy:%d:%d", groupID, policy.Version)
			p.Source = "routing_billing_policy"
			p.BillingMode = policy.BillingMode
		}
	}
	if uc.userPriceOverrides != nil && userID > 0 {
		override, err := uc.resolveUserPriceOverride(ctx, userID, groupID)
		if err != nil {
			return nil, err
		}
		if override != nil {
			p.PriceRatio = override.PriceRatio
			p.Version = userPriceVersion(override)
			p.Source = "user_routing_price_override"
			p.UserPrice = override
		}
	}
	if uc.subscription != nil && userID > 0 {
		sub, err := uc.subscription.GetActiveSubscriptionForUser(ctx, userID)
		if errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
			return p, nil
		}
		if err != nil {
			return nil, err
		}
		p.SubscriptionCovered = sub != nil && sub.Contract.Covers(groupID) && p.BillingMode != routing.WalletOnly
	}
	return p, nil
}
