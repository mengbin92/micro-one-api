package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

type mockSubscriptionRepo struct {
	groups        map[int64]*SubscriptionGroup
	subscriptions map[int64]*UserSubscription
	nextGroupID   int64
	nextSubID     int64
}

func newMockSubscriptionRepo() *mockSubscriptionRepo {
	return &mockSubscriptionRepo{
		groups:        map[int64]*SubscriptionGroup{},
		subscriptions: map[int64]*UserSubscription{},
		nextGroupID:   1,
		nextSubID:     1,
	}
}

func (m *mockSubscriptionRepo) CreateSubscription(ctx context.Context, subscription *UserSubscription) error {
	subscription.ID = m.nextSubID
	m.nextSubID++
	cloned := *subscription
	m.subscriptions[subscription.ID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) UpdateSubscription(ctx context.Context, subscription *UserSubscription) error {
	cloned := *subscription
	m.subscriptions[subscription.ID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) DeleteSubscription(ctx context.Context, subscriptionID int64) error {
	delete(m.subscriptions, subscriptionID)
	return nil
}

func (m *mockSubscriptionRepo) GetSubscriptionByID(ctx context.Context, subscriptionID int64) (*UserSubscription, error) {
	subscription, ok := m.subscriptions[subscriptionID]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	cloned := *subscription
	return &cloned, nil
}

func (m *mockSubscriptionRepo) ListSubscriptionsByUser(ctx context.Context, userID int64) ([]*UserSubscription, error) {
	var result []*UserSubscription
	for _, subscription := range m.subscriptions {
		if subscription.UserID != userID {
			continue
		}
		cloned := *subscription
		result = append(result, &cloned)
	}
	return result, nil
}

func (m *mockSubscriptionRepo) ListActiveSubscriptions(ctx context.Context) ([]*UserSubscription, error) {
	var result []*UserSubscription
	for _, subscription := range m.subscriptions {
		if subscription.Status != SubscriptionStatusActive {
			continue
		}
		cloned := *subscription
		result = append(result, &cloned)
	}
	return result, nil
}

func (m *mockSubscriptionRepo) ListAllSubscriptions(ctx context.Context) ([]*UserSubscription, error) {
	var result []*UserSubscription
	for _, subscription := range m.subscriptions {
		cloned := *subscription
		result = append(result, &cloned)
	}
	return result, nil
}

func (m *mockSubscriptionRepo) GetActiveSubscriptionByUser(ctx context.Context, userID int64) (*UserSubscription, error) {
	for _, subscription := range m.subscriptions {
		if subscription.UserID == userID && subscription.Status == SubscriptionStatusActive {
			cloned := *subscription
			return &cloned, nil
		}
	}
	return nil, ErrSubscriptionNotFound
}

func (m *mockSubscriptionRepo) AddUsage(ctx context.Context, userID int64, costUSD float64, now int64) error {
	for id, subscription := range m.subscriptions {
		if subscription.UserID != userID || subscription.Status != SubscriptionStatusActive {
			continue
		}
		rolled := RollUsageWindows(subscription, now)
		rolled.DailyUsageUSD += costUSD
		rolled.WeeklyUsageUSD += costUSD
		rolled.MonthlyUsageUSD += costUSD
		rolled.UpdatedAt = now
		m.subscriptions[id] = rolled
		return nil
	}
	return ErrSubscriptionNotFound
}

func (m *mockSubscriptionRepo) AddUsageByIDInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error {
	subscription, ok := m.subscriptions[subscriptionID]
	if !ok {
		return ErrSubscriptionNotFound
	}
	subscription.DailyUsageUSD += costUSD
	subscription.WeeklyUsageUSD += costUSD
	subscription.MonthlyUsageUSD += costUSD
	subscription.UpdatedAt = now
	cloned := *subscription
	m.subscriptions[subscriptionID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) GetByIDInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64) (*UserSubscription, error) {
	subscription, ok := m.subscriptions[subscriptionID]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	cloned := *subscription
	return &cloned, nil
}

func (m *mockSubscriptionRepo) CreateGroup(ctx context.Context, group *SubscriptionGroup) error {
	group.ID = m.nextGroupID
	m.nextGroupID++
	cloned := *group
	m.groups[group.ID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) UpdateGroup(ctx context.Context, group *SubscriptionGroup) error {
	cloned := *group
	m.groups[group.ID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) DeleteGroup(ctx context.Context, groupID int64) error {
	delete(m.groups, groupID)
	return nil
}

func (m *mockSubscriptionRepo) GetGroupByID(ctx context.Context, groupID int64) (*SubscriptionGroup, error) {
	group, ok := m.groups[groupID]
	if !ok {
		return nil, ErrSubscriptionGroupNotFound
	}
	cloned := *group
	return &cloned, nil
}

func (m *mockSubscriptionRepo) GetGroupByName(ctx context.Context, name string) (*SubscriptionGroup, error) {
	for _, group := range m.groups {
		if group.Name == name {
			cloned := *group
			return &cloned, nil
		}
	}
	return nil, ErrSubscriptionGroupNotFound
}

func (m *mockSubscriptionRepo) ListGroups(ctx context.Context) ([]*SubscriptionGroup, error) {
	result := make([]*SubscriptionGroup, 0, len(m.groups))
	for _, group := range m.groups {
		cloned := *group
		result = append(result, &cloned)
	}
	return result, nil
}

func TestSubscriptionUsecase_AssignAndQuotaFlow(t *testing.T) {
	repo := newMockSubscriptionRepo()
	requireGroup := &SubscriptionGroup{
		Name:            "pro",
		Platform:        "openai",
		Status:          SubscriptionGroupStatusEnabled,
		DailyLimitUSD:   ptrFloat64(10),
		WeeklyLimitUSD:  ptrFloat64(70),
		MonthlyLimitUSD: ptrFloat64(300),
	}
	if err := repo.CreateGroup(context.Background(), requireGroup); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(1000, 0) }

	sub, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: requireGroup.ID, ExpiresAt: 2000, SubscriptionName: "alice-pro",
	})
	if err != nil {
		t.Fatalf("Assign() error = %v", err)
	}
	if sub.Status != SubscriptionStatusActive {
		t.Fatalf("status = %s, want active", sub.Status)
	}

	result, err := uc.CheckQuota(context.Background(), 1, 2.5)
	if err != nil {
		t.Fatalf("CheckQuota() error = %v", err)
	}
	if !result.Allowed {
		t.Fatalf("quota should allow, got %+v", result)
	}

	if err := uc.RecordUsage(context.Background(), 1, 2.5); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	progress, err := uc.GetProgress(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetProgress() error = %v", err)
	}
	if progress.DailyUsed.Used != 2.5 {
		t.Fatalf("daily used = %v, want 2.5", progress.DailyUsed.Used)
	}
}

func TestSubscriptionUsecase_AssignOrExtendSameGroup(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(1000, 0) }

	sub, reused, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 2000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("AssignOrExtend() create error = %v", err)
	}
	if reused {
		t.Fatalf("first AssignOrExtend reused = true, want false")
	}

	sub, reused, err = uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1100, ExpiresAt: 1600, SubscriptionName: "pro-renew",
	})
	if err != nil {
		t.Fatalf("AssignOrExtend() renew error = %v", err)
	}
	if !reused {
		t.Fatalf("renew reused = false, want true")
	}
	if sub.ExpiresAt != 2500 {
		t.Fatalf("expires_at = %d, want 2500", sub.ExpiresAt)
	}
	if sub.SubscriptionName != "pro-renew" {
		t.Fatalf("subscription_name = %q, want pro-renew", sub.SubscriptionName)
	}
}

func TestSubscriptionUsecase_AssignOrExtendRejectsDifferentActiveGroup(t *testing.T) {
	repo := newMockSubscriptionRepo()
	groupA := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	groupB := &SubscriptionGroup{Name: "team", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupA); err != nil {
		t.Fatalf("CreateGroup A error = %v", err)
	}
	if err := repo.CreateGroup(context.Background(), groupB); err != nil {
		t.Fatalf("CreateGroup B error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)

	if _, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: groupA.ID, StartsAt: 1000, ExpiresAt: 2000,
	}); err != nil {
		t.Fatalf("AssignOrExtend() create error = %v", err)
	}

	_, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: groupB.ID, StartsAt: 1000, ExpiresAt: 2000,
	})
	if !errors.Is(err, ErrSubscriptionAlreadyAssigned) {
		t.Fatalf("AssignOrExtend() error = %v, want ErrSubscriptionAlreadyAssigned", err)
	}
}

func TestSubscriptionUsecase_RejectsDuplicateAssignmentAndRevokedExtend(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(1000, 0) }

	_, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{UserID: 1, GroupID: group.ID, ExpiresAt: 2000})
	if err != nil {
		t.Fatalf("Assign() error = %v", err)
	}
	_, err = uc.Assign(context.Background(), &AssignSubscriptionRequest{UserID: 1, GroupID: group.ID, ExpiresAt: 2000})
	if !errors.Is(err, ErrSubscriptionAlreadyAssigned) {
		t.Fatalf("Assign() error = %v, want duplicate error", err)
	}

	if err := uc.Revoke(context.Background(), 1, "manual"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	err = uc.Extend(context.Background(), 1, 3000)
	if !errors.Is(err, ErrSubscriptionRevoked) {
		t.Fatalf("Extend() error = %v, want revoked error", err)
	}
}

func ptrFloat64(v float64) *float64 {
	return &v
}
