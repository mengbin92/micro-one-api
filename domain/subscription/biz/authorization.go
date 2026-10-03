package biz

import (
	"context"
	"micro-one-api/domain/authorization"
)

// Authorization is supplied by the embedding consumer, which is the actual
// writer of the shared subscription tables. It never impersonates billing.
func (uc *GroupUsecase) SetAuthorization(r authorization.Resolver, consumer string) {
	uc.authorization = r
	uc.consumer = consumer
}
func (uc *PlanUsecase) SetAuthorization(r authorization.Resolver, consumer string) {
	uc.authorization = r
	uc.consumer = consumer
}
func (uc *SubscriptionUsecase) SetAuthorization(r authorization.Resolver, consumer string) {
	uc.authorization = r
	uc.consumer = consumer
}

type selfRequestKey struct{}

func WithSelfRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, selfRequestKey{}, true)
}
func IsSelfRequest(ctx context.Context) bool { v, _ := ctx.Value(selfRequestKey{}).(bool); return v }
func prepareSubscription(ctx context.Context, r authorization.Resolver, consumer, resource, action string) (context.Context, error) {
	if !authorization.External(ctx) {
		return ctx, nil
	}
	point := "subscription." + resource
	if consumer == "admin" {
		point = "admin." + point
	}
	op := "subscription." + map[string]string{"quota_policies": "quota_policy", "plans": "plan", "user_subscriptions": "user_subscription"}[resource] + "." + action
	if self, _ := ctx.Value(selfRequestKey{}).(bool); self && resource == "user_subscriptions" {
		return authorization.PrepareSelf(ctx, r, "admin.subscription.self", op)
	}
	return authorization.Prepare(ctx, r, point, op)
}
func subscriptionFacts(sub *UserSubscription) authorization.ObjectFacts {
	if sub == nil {
		return authorization.ObjectFacts{}
	}
	return authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: sub.ID, OwnerUserID: sub.UserID}
}

type expectedRevisionKey struct{}

func WithExpectedRevision(ctx context.Context, version int64) context.Context {
	return context.WithValue(ctx, expectedRevisionKey{}, version)
}
func ExpectedRevision(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(expectedRevisionKey{}).(int64)
	return v, ok
}
