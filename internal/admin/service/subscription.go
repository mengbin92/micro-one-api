package service

import (
	"context"
	"errors"

	subscriptionbiz "micro-one-api/internal/subscription/biz"
)

var ErrSubscriptionServiceNotConfigured = errors.New("subscription service not configured")

func (s *AdminService) AssignSubscription(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.Assign(ctx, req)
}

func (s *AdminService) RevokeSubscription(ctx context.Context, id int64, reason string) error {
	if s == nil || s.subscriptionUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.Revoke(ctx, id, reason)
}

func (s *AdminService) ExtendSubscription(ctx context.Context, id int64, expiresAt int64) error {
	if s == nil || s.subscriptionUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.Extend(ctx, id, expiresAt)
}

func (s *AdminService) ResetSubscriptionQuota(ctx context.Context, id int64, scope string) error {
	if s == nil || s.subscriptionUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.ResetQuota(ctx, id, scope)
}

func (s *AdminService) GetSubscriptionProgress(ctx context.Context, userID int64) (*subscriptionbiz.SubscriptionProgress, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.GetProgress(ctx, userID)
}

func (s *AdminService) ListUserSubscriptions(ctx context.Context, userID int64) ([]*subscriptionbiz.UserSubscription, error) {
	if s == nil || s.subscriptionUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.subscriptionUc.ListByUser(ctx, userID)
}

func (s *AdminService) CreateSubscriptionGroup(ctx context.Context, group *subscriptionbiz.SubscriptionGroup) error {
	if s == nil || s.groupUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Create(ctx, group)
}

func (s *AdminService) UpdateSubscriptionGroup(ctx context.Context, group *subscriptionbiz.SubscriptionGroup) error {
	if s == nil || s.groupUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Update(ctx, group)
}

func (s *AdminService) DeleteSubscriptionGroup(ctx context.Context, groupID int64) error {
	if s == nil || s.groupUc == nil {
		return ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Delete(ctx, groupID)
}

func (s *AdminService) GetSubscriptionGroup(ctx context.Context, groupID int64) (*subscriptionbiz.SubscriptionGroup, error) {
	if s == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.Get(ctx, groupID)
}

func (s *AdminService) ListSubscriptionGroups(ctx context.Context) ([]*subscriptionbiz.SubscriptionGroup, error) {
	if s == nil || s.groupUc == nil {
		return nil, ErrSubscriptionServiceNotConfigured
	}
	return s.groupUc.List(ctx)
}
