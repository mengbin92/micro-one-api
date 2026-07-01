package biz

import "context"

type SubscriptionRepository interface {
	CreateSubscription(ctx context.Context, subscription *UserSubscription) error
	UpdateSubscription(ctx context.Context, subscription *UserSubscription) error
	DeleteSubscription(ctx context.Context, subscriptionID int64) error
	GetSubscriptionByID(ctx context.Context, subscriptionID int64) (*UserSubscription, error)
	ListSubscriptionsByUser(ctx context.Context, userID int64) ([]*UserSubscription, error)
	ListActiveSubscriptions(ctx context.Context) ([]*UserSubscription, error)
	GetActiveSubscriptionByUser(ctx context.Context, userID int64) (*UserSubscription, error)
}

type GroupRepository interface {
	CreateGroup(ctx context.Context, group *SubscriptionGroup) error
	UpdateGroup(ctx context.Context, group *SubscriptionGroup) error
	DeleteGroup(ctx context.Context, groupID int64) error
	GetGroupByID(ctx context.Context, groupID int64) (*SubscriptionGroup, error)
	GetGroupByName(ctx context.Context, name string) (*SubscriptionGroup, error)
	ListGroups(ctx context.Context) ([]*SubscriptionGroup, error)
}
