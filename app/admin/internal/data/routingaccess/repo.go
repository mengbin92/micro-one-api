package routingaccess

import (
	"context"
	"fmt"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/routingdto"
	"time"
)

type repo struct {
	identity identityv1.IdentityServiceClient
	channel  channelv1.ChannelServiceClient
	billing  billingv1.BillingServiceClient
}

func NewRepo(i identityv1.IdentityServiceClient, c channelv1.ChannelServiceClient, b billingv1.BillingServiceClient) biz.RoutingAccessRepo {
	return &repo{i, c, b}
}
func (r *repo) Facts(ctx context.Context, id int64) (*routing.SubjectFacts, error) {
	p, err := r.identity.GetUserRoutingFacts(ctx, &identityv1.GetUserRoutingFactsRequest{UserId: id})
	if err != nil {
		return nil, err
	}
	f := routingdto.FactsFromProto(p.GetFacts())
	if f == nil || f.AccessRevision <= 0 {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	for _, t := range p.Tokens {
		f.TokenReferences = append(f.TokenReferences, routing.TokenReference{ID: t.Id, Name: t.Name, Mode: t.Mode, GroupID: t.GroupId, Revision: t.Revision})
	}
	return f, nil
}
func (r *repo) Change(ctx context.Context, c biz.RoutingAccessChange) (*routing.SubjectFacts, error) {
	p, err := r.identity.UpdateUserRoutingAccess(ctx, &identityv1.UpdateUserRoutingAccessRequest{UserId: c.UserID, ExpectedRevision: c.ExpectedRevision, RoutingGroupId: c.GroupID, Operation: c.Operation, GroupKey: c.GroupKey, SourceType: c.SourceType, SourceRef: c.SourceRef, StartsAt: c.StartsAt, ExpiresAt: c.ExpiresAt, PublicGroupAccess: c.PublicGroupAccess})
	if err != nil {
		return nil, err
	}
	f := routingdto.FactsFromProto(p.GetFacts())
	if f == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	for _, t := range p.Tokens {
		f.TokenReferences = append(f.TokenReferences, routing.TokenReference{ID: t.Id, Name: t.Name, Mode: t.Mode, GroupID: t.GroupId, Revision: t.Revision})
	}
	return f, nil
}
func (r *repo) CreateToken(ctx context.Context, id int64, name, mode string, group int64) (*biz.RoutingToken, error) {
	p, err := r.identity.CreateAccessToken(ctx, &identityv1.CreateAccessTokenRequest{UserId: id, Name: name, RoutingMode: mode, RoutingGroupId: group})
	if err != nil {
		return nil, err
	}
	if !p.GetSuccess() {
		return nil, fmt.Errorf("%s", p.GetMessage())
	}
	return &biz.RoutingToken{ID: p.TokenId, Key: p.Token, Name: name, Mode: mode, GroupID: group, Revision: 1, CreatedAt: time.Now().Unix()}, nil
}
func (r *repo) SetToken(ctx context.Context, user, token int64, mode string, group, rev int64) (int64, error) {
	p, err := r.identity.SetTokenRouting(ctx, &identityv1.SetTokenRoutingRequest{UserId: user, TokenId: token, RoutingMode: mode, RoutingGroupId: group, ExpectedRevision: rev})
	if err != nil {
		return 0, err
	}
	return p.GetRevision(), nil
}
func (r *repo) Price(ctx context.Context, group, user int64) (biz.RoutingPrice, error) {
	p, err := r.billing.GetRoutingGroupPrice(ctx, &billingv1.GetRoutingGroupPriceRequest{RoutingGroupId: group, UserId: user})
	if err != nil {
		return biz.RoutingPrice{}, err
	}
	return biz.RoutingPrice{Ratio: p.PriceRatio, Version: p.Version, Source: p.Source, BillingMode: p.BillingMode, SubscriptionCovered: p.SubscriptionCovered}, nil
}
func (r *repo) Models(ctx context.Context, groupID int64, key string) ([]string, error) {
	p, err := r.channel.ListAvailableModels(ctx, &channelv1.ListAvailableModelsRequest{Group: key, RoutingGroupId: groupID})
	if err != nil {
		return nil, err
	}
	return p.GetModels(), nil
}
func (r *repo) CheckCapabilities(ctx context.Context) error {
	p, err := r.billing.GetRoutingCapabilities(ctx, &billingv1.GetRoutingCapabilitiesRequest{})
	if err != nil {
		return err
	}
	if !p.GetFixedRouting() || p.GetRequestSnapshotVersion() != 2 || (subscriptionbiz.EntitlementsEnabled() && !p.GetSubscriptionContracts()) {
		return biz.ErrRoutingGroupUnavailable
	}
	return nil
}

func (r *repo) SetState(ctx context.Context, id, revision int64, status, access string) error {
	_, err := r.channel.SetRoutingGroupState(ctx, &channelv1.SetRoutingGroupStateRequest{Id: id, ExpectedRevision: revision, Status: status, AccessMode: access})
	return err
}

func (r *repo) GetBillingPolicy(ctx context.Context, id int64) (*routing.BillingPolicy, error) {
	p, err := r.billing.GetRoutingBillingPolicy(ctx, &billingv1.GetRoutingBillingPolicyRequest{RoutingGroupId: id})
	if err != nil {
		return nil, err
	}
	return &routing.BillingPolicy{GroupID: p.RoutingGroupId, Version: p.Version, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio, EffectiveAt: p.EffectiveAt}, nil
}
func (r *repo) PublishBillingPolicy(ctx context.Context, p *routing.BillingPolicy, expected int64) error {
	result, err := r.billing.PublishRoutingBillingPolicy(ctx, &billingv1.PublishRoutingBillingPolicyRequest{RoutingGroupId: p.GroupID, ExpectedVersion: expected, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio})
	if err != nil {
		return err
	}
	p.Version, p.EffectiveAt = result.Version, result.EffectiveAt
	return nil
}
