package service

import (
	"context"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
)

func (s *BillingService) GetRoutingGroupPrice(ctx context.Context, req *billingv1.GetRoutingGroupPriceRequest) (*billingv1.GetRoutingGroupPriceReply, error) {
	p, err := s.uc.RoutingGroupPrice(ctx, req.RoutingGroupId, req.UserId)
	if err != nil {
		return nil, err
	}
	reply := &billingv1.GetRoutingGroupPriceReply{GroupKey: p.GroupKey, PriceRatio: p.PriceRatio, Version: p.Version, Source: p.Source, BillingMode: p.BillingMode, SubscriptionCovered: p.SubscriptionCovered}
	if p.UserPrice != nil {
		reply.UserPriceRatio = &p.UserPrice.PriceRatio
		reply.UserPriceVersion = &p.UserPrice.Version
	}
	return reply, nil
}

func (s *BillingService) GetRoutingBillingPolicy(ctx context.Context, req *billingv1.GetRoutingBillingPolicyRequest) (*billingv1.RoutingBillingPolicy, error) {
	p, err := s.uc.RoutingBillingPolicy(ctx, req.RoutingGroupId)
	if err != nil {
		return nil, err
	}
	return &billingv1.RoutingBillingPolicy{RoutingGroupId: p.GroupID, Version: p.Version, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio, EffectiveAt: p.EffectiveAt}, nil
}
func (s *BillingService) PublishRoutingBillingPolicy(ctx context.Context, req *billingv1.PublishRoutingBillingPolicyRequest) (*billingv1.RoutingBillingPolicy, error) {
	p := &routing.BillingPolicy{GroupID: req.RoutingGroupId, BillingMode: req.BillingMode, PriceRatio: req.PriceRatio}
	if err := s.uc.PublishRoutingBillingPolicy(ctx, p, req.ExpectedVersion); err != nil {
		return nil, err
	}
	return &billingv1.RoutingBillingPolicy{RoutingGroupId: p.GroupID, Version: p.Version, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio, EffectiveAt: p.EffectiveAt}, nil
}

func (s *BillingService) SetSubscriptionCommerce(uc *biz.SubscriptionCommerce) { s.commerceUc = uc }
func (s *BillingService) ExecuteSubscriptionCommerce(ctx context.Context, req *billingv1.SubscriptionCommerceRequest) (*billingv1.SubscriptionCommerceReply, error) {
	if s.commerceUc == nil {
		return nil, biz.ErrRequestSnapshotUnavailable
	}
	result, err := s.commerceUc.Execute(ctx, biz.SubscriptionCommerceRequest{UserID: req.UserId, RequestID: req.RequestId, PlanID: req.PlanId, FromSubscriptionID: req.FromSubscriptionId, ChangePolicy: req.ChangePolicy})
	if err != nil {
		return nil, err
	}
	raw, err := jsonx.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &billingv1.SubscriptionCommerceReply{ResultJson: string(raw)}, nil
}
