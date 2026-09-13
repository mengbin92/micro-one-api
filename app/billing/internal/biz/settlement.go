package biz

import (
	"context"
	"errors"
	"strconv"

	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// RoutingSettlement is the Phase F ordered-routing settlement qualification
// probe result. Relay terminates (never advances to a pricier group) when a
// candidate group is not settleable for the user.
type RoutingSettlement struct {
	Allowed bool
	Reason  string
}

// CheckRoutingSettlement answers whether the user could settle a request in
// the given routing group right now, from the published billing policy and
// current wallet/subscription state. It is a plan-time gate only: reserve
// remains the fail-closed settlement authority.
func (uc *BillingUsecase) CheckRoutingSettlement(ctx context.Context, userID, groupID int64) (*RoutingSettlement, error) {
	if !uc.RoutingSnapshotsAvailable() {
		return nil, ErrRequestSnapshotUnavailable
	}
	if userID <= 0 || groupID <= 0 {
		return nil, ErrRoutingContextInvalid
	}
	g, err := uc.routingGroups.GetRoutingGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil || g.Status != "enabled" {
		return nil, ErrRoutingContextInvalid
	}
	mode := routing.SubscriptionFirst
	if uc.routingPolicies != nil {
		policy, err := uc.routingPolicies.Get(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if policy != nil {
			mode = policy.BillingMode
		}
	}
	hasWallet, err := uc.hasPositiveBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	hasCoverage, hasQuota, err := uc.subscriptionCoverage(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	s := &RoutingSettlement{Allowed: true}
	switch mode {
	case routing.WalletOnly:
		s.Allowed = hasWallet
		s.Reason = "wallet_only group requires a positive balance"
	case routing.SubscriptionOnly:
		s.Allowed = hasCoverage && hasQuota
		s.Reason = "subscription-only group requires an active subscription with remaining quota"
	default: // subscription_first
		s.Allowed = hasWallet || hasCoverage && hasQuota
		s.Reason = "subscription_first group requires a subscription with quota or a positive balance"
	}
	return s, nil
}

func (uc *BillingUsecase) hasPositiveBalance(ctx context.Context, userID int64) (bool, error) {
	if uc.accountRepo == nil {
		return false, nil
	}
	account, err := uc.accountRepo.GetAccountSnapshot(ctx, strconv.FormatInt(userID, 10))
	if err != nil {
		return false, err
	}
	return account != nil && account.Balance > 0, nil
}

// subscriptionCoverage reports whether the user's active subscription covers
// the routing group and still has window quota remaining. No limits at all
// means unlimited. A missing subscription is (false, false, nil).
func (uc *BillingUsecase) subscriptionCoverage(ctx context.Context, userID, groupID int64) (covers, quota bool, err error) {
	if uc.subscription == nil {
		return false, false, nil
	}
	sub, err := uc.subscription.GetActiveSubscriptionForUser(ctx, userID)
	if errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if sub == nil || !sub.Contract.Covers(groupID) {
		return false, false, nil
	}
	group, err := uc.subscription.GetGroupForSubscription(ctx, sub)
	if err != nil {
		return false, false, err
	}
	if group == nil {
		return true, true, nil
	}
	// Match reserve's rolling windows and strictest-limit semantics.
	sub = subscriptionbiz.RollUsageWindowsPure(sub, uc.Now().Unix())
	for _, window := range []struct {
		limit *float64
		usage float64
	}{
		{group.DailyLimitUSD, sub.DailyUsageUSD},
		{group.WeeklyLimitUSD, sub.WeeklyUsageUSD},
		{group.MonthlyLimitUSD, sub.MonthlyUsageUSD},
	} {
		if window.limit == nil {
			continue
		}
		if window.usage >= *window.limit {
			return true, false, nil
		}
	}
	return true, true, nil
}
