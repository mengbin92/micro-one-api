package biz

import (
	"context"

	"gorm.io/gorm"
)

type SubscriptionRepository interface {
	CreateSubscription(ctx context.Context, subscription *UserSubscription) error
	UpdateSubscription(ctx context.Context, subscription *UserSubscription) error
	UpdateSubscriptionInTx(ctx context.Context, tx *gorm.DB, subscription *UserSubscription) error
	DeleteSubscription(ctx context.Context, subscriptionID int64) error
	GetSubscriptionByID(ctx context.Context, subscriptionID int64) (*UserSubscription, error)
	ListSubscriptionsByUser(ctx context.Context, userID int64) ([]*UserSubscription, error)
	ListActiveSubscriptions(ctx context.Context) ([]*UserSubscription, error)
	// ListAllSubscriptions returns every subscription regardless of user or
	// status, newest first, so admins can browse without knowing a user id.
	ListAllSubscriptions(ctx context.Context) ([]*UserSubscription, error)
	GetActiveSubscriptionByUser(ctx context.Context, userID int64) (*UserSubscription, error)
	// AddUsage atomically rolls the active subscription's usage windows relative
	// to now (unix seconds) and adds costUSD to every window. Implementations
	// must perform the read-roll-increment as a single atomic unit so concurrent
	// callers cannot lose each other's increments.
	AddUsage(ctx context.Context, userID int64, costUSD float64, now int64) error
	// AddUsageByIDInTx is the row-locked variant used by the dual-track commit
	// pipeline. It performs the read-roll-increment against the subscription
	// identified by id (NOT userID) inside the caller's transaction so the
	// commit and the subscription write either both succeed or both fail.
	// Implementations must take a row lock on the subscription so the cost
	// is appended to the same window the dual-track pre-deduction reserved
	// against.
	AddUsageByIDInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error
	// GetByIDInTx is a row-locked read of the subscription inside the
	// caller's transaction. Used by the dual-track commit pipeline so the
	// committed cost is applied to the same subscription snapshot the
	// pre-deduction locked.
	GetByIDInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64) (*UserSubscription, error)
}

type GroupRepository interface {
	CreateGroup(ctx context.Context, group *SubscriptionGroup) error
	UpdateGroup(ctx context.Context, group *SubscriptionGroup) error
	DeleteGroup(ctx context.Context, groupID int64) error
	GetGroupByID(ctx context.Context, groupID int64) (*SubscriptionGroup, error)
	GetGroupByName(ctx context.Context, name string) (*SubscriptionGroup, error)
	ListGroups(ctx context.Context) ([]*SubscriptionGroup, error)
}

type PlanRepository interface {
	CreatePlan(ctx context.Context, plan *SubscriptionPlan) error
	UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error
	DeletePlan(ctx context.Context, planID int64) error
	GetPlanByID(ctx context.Context, planID int64) (*SubscriptionPlan, error)
	ListPlans(ctx context.Context) ([]*SubscriptionPlan, error)
	ListPlansForSale(ctx context.Context) ([]*SubscriptionPlan, error)
}
