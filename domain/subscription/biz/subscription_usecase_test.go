package biz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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

func (m *mockSubscriptionRepo) CreateSubscriptionInTx(ctx context.Context, tx Tx, subscription *UserSubscription) error {
	return m.CreateSubscription(ctx, subscription)
}

func (m *mockSubscriptionRepo) UpdateSubscription(ctx context.Context, subscription *UserSubscription) error {
	cloned := *subscription
	m.subscriptions[subscription.ID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) UpdateSubscriptionInTx(ctx context.Context, tx Tx, subscription *UserSubscription) error {
	return m.UpdateSubscription(ctx, subscription)
}

// UpdateSubscriptionFields mirrors the selective write of the real repo for
// the in-memory mock: it applies only the named fields onto the stored row so
// tests observe the same narrow-write semantics as production
// (code-review 2026-07-30 domain-H1).
func (m *mockSubscriptionRepo) UpdateSubscriptionFields(ctx context.Context, subscription *UserSubscription, fields []SubscriptionField) error {
	if subscription == nil {
		return errors.New("nil subscription")
	}
	existing, ok := m.subscriptions[subscription.ID]
	if !ok {
		return ErrSubscriptionNotFound
	}
	merged := *existing
	for _, f := range fields {
		switch f {
		case SubscriptionFieldStatus:
			merged.Status = subscription.Status
		case SubscriptionFieldExpiresAt:
			merged.ExpiresAt = subscription.ExpiresAt
		case SubscriptionFieldSubscriptionName:
			merged.SubscriptionName = subscription.SubscriptionName
		case SubscriptionFieldGroupID:
			merged.GroupID = subscription.GroupID
		case SubscriptionFieldMetadata:
			merged.Metadata = subscription.Metadata
		case SubscriptionFieldRenewalStrategy:
			merged.RenewalStrategy = subscription.RenewalStrategy
		case SubscriptionFieldUsageAll:
			merged.DailyUsageUSD = subscription.DailyUsageUSD
			merged.WeeklyUsageUSD = subscription.WeeklyUsageUSD
			merged.MonthlyUsageUSD = subscription.MonthlyUsageUSD
			merged.DailyWindowStart = subscription.DailyWindowStart
			merged.WeeklyWindowStart = subscription.WeeklyWindowStart
			merged.MonthlyWindowStart = subscription.MonthlyWindowStart
		}
	}
	merged.UpdatedAt = subscription.UpdatedAt
	cloned := merged
	m.subscriptions[subscription.ID] = &cloned
	return nil
}

func (m *mockSubscriptionRepo) UpdateSubscriptionFieldsInTx(ctx context.Context, tx Tx, subscription *UserSubscription, fields []SubscriptionField) error {
	return m.UpdateSubscriptionFields(ctx, subscription, fields)
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

func (m *mockSubscriptionRepo) GetActiveSubscriptionByUserInTx(ctx context.Context, tx Tx, userID int64) (*UserSubscription, error) {
	return m.GetActiveSubscriptionByUser(ctx, userID)
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

func (m *mockSubscriptionRepo) AddUsageByIDInTx(ctx context.Context, tx Tx, subscriptionID int64, costUSD float64, now int64) error {
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

func (m *mockSubscriptionRepo) GetByIDInTx(ctx context.Context, tx Tx, subscriptionID int64) (*UserSubscription, error) {
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

// TestAssignOrExtend_AccumulatesRemainingTime (review §6 regression for H3):
// renewing a subscription that still has remaining time must ADD the renewal
// duration to the existing expiry, not overwrite it with now+duration.
// Previously a renewal whose duration was shorter than the remaining window
// truncated the user's entitlement (H3).
func TestAssignOrExtend_AccumulatesRemainingTime(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	// Fix now so the test is deterministic. now=5000.
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	// Create a subscription expiring at 9000 (4000s of remaining time).
	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 9000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("initial assign: %v", err)
	}
	origExpires := sub.ExpiresAt // 9000

	// Renew for 30 days (30*86400) starting now=5000. A renewal must accumulate:
	// new expires = max(9000, 5000) + 30d = 9000 + 30d, NOT 5000 + 30d.
	renewed, reused, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 5000, ExpiresAt: 5000 + 30*86400, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !reused {
		t.Fatal("renewal should reuse the active subscription, not create a new one")
	}
	want := origExpires + int64(30*86400)
	if renewed.ExpiresAt != want {
		t.Fatalf("renewal expiry = %d, want %d (accumulated); orig=%d", renewed.ExpiresAt, want, origExpires)
	}
}

// TestAssignOrExtend_RenewalAfterExpiryStartsFromNow (review §6 regression for H3):
// when the active subscription has already expired, the renewal starts from now
// (max(active.ExpiresAt, now) = now), so the user does not get credit for time
// they already consumed past expiry.
func TestAssignOrExtend_RenewalAfterExpiryStartsFromNow(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(10000, 0) }

	// Active subscription expired at 5000 (now=10000).
	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 5000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if sub.ExpiresAt != 5000 {
		t.Fatalf("initial expiry = %d, want 5000", sub.ExpiresAt)
	}

	// Renew for 30 days. base = max(5000, 10000) = 10000.
	renewed, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 10000, ExpiresAt: 10000 + 30*86400, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	want := int64(10000 + 30*86400)
	if renewed.ExpiresAt != want {
		t.Fatalf("renewal expiry = %d, want %d (now+30d, no credit for expired time)", renewed.ExpiresAt, want)
	}
}

func TestSubscriptionUsecase_GetProgressNextRefresh(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{
		Name:            "pro",
		DisplayName:     "Pro 套餐",
		Platform:        "openai",
		Status:          SubscriptionGroupStatusEnabled,
		DailyLimitUSD:   ptrFloat64(10),
		WeeklyLimitUSD:  ptrFloat64(70),
		MonthlyLimitUSD: ptrFloat64(300),
	}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	// now is far in the future so the windows rolled at some point; we verify
	// the next_refresh = window_start + period.
	const nowSec = 1_000_000
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(nowSec, 0) }

	if _, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, ExpiresAt: nowSec + 86400, SubscriptionName: "pro",
	}); err != nil {
		t.Fatalf("Assign() error = %v", err)
	}

	// Simulate usage so the windows are non-zero.
	if err := uc.RecordUsage(context.Background(), 1, 2.5); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	progress, err := uc.GetProgress(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetProgress() error = %v", err)
	}

	// Group display name surfaces for tooling / cc-switch "planName".
	if progress.SubscriptionName != "Pro 套餐" {
		t.Fatalf("subscription_name = %q, want %q", progress.SubscriptionName, "Pro 套餐")
	}
	if progress.GroupID != group.ID {
		t.Fatalf("group_id = %d, want %d", progress.GroupID, group.ID)
	}

	// next_refresh = window_start + period. After Assign at nowSec the windows
	// start at nowSec; RecordUsage rolls if needed but at nowSec the window is
	// fresh so start stays nowSec.
	dailySec := int64(quotaDailyWindow.Seconds())
	weeklySec := int64(quotaWeeklyWindow.Seconds())
	monthlySec := int64(quotaMonthlyWindow.Seconds())

	if progress.DailyUsed.NextRefresh != nowSec+dailySec {
		t.Fatalf("daily next_refresh = %d, want %d", progress.DailyUsed.NextRefresh, nowSec+dailySec)
	}
	if progress.WeeklyUsed.NextRefresh != nowSec+weeklySec {
		t.Fatalf("weekly next_refresh = %d, want %d", progress.WeeklyUsed.NextRefresh, nowSec+weeklySec)
	}
	if progress.MonthlyUsed.NextRefresh != nowSec+monthlySec {
		t.Fatalf("monthly next_refresh = %d, want %d", progress.MonthlyUsed.NextRefresh, nowSec+monthlySec)
	}
}

func TestSubscriptionUsecase_GetProgressRollsWindowAndResetsNextRefresh(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{
		Name:           "pro",
		Platform:       "openai",
		Status:         SubscriptionGroupStatusEnabled,
		DailyLimitUSD:  ptrFloat64(10),
		WeeklyLimitUSD: ptrFloat64(70),
	}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	// Assign at t0, then jump time past the daily window so it rolls.
	const t0 = 1000
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(t0, 0) }
	if _, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, ExpiresAt: t0 + 86400*60,
	}); err != nil {
		t.Fatalf("Assign() error = %v", err)
	}
	if err := uc.RecordUsage(context.Background(), 1, 2.5); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	// Advance time past the daily window (25h later).
	const nowSec = t0 + 25*3600
	uc.now = func() time.Time { return time.Unix(nowSec, 0) }

	progress, err := uc.GetProgress(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetProgress() error = %v", err)
	}
	// Daily usage should have rolled to 0.
	if progress.DailyUsed.Used != 0 {
		t.Fatalf("daily used = %v, want 0 after roll", progress.DailyUsed.Used)
	}
	// After rolling, the new window stays aligned to the subscription start
	// anchor, so next_refresh is t0 + 48h rather than now + 24h.
	dailySec := int64(quotaDailyWindow.Seconds())
	if progress.DailyUsed.NextRefresh != t0+2*dailySec {
		t.Fatalf("daily next_refresh = %d, want %d (anchored rolled window)", progress.DailyUsed.NextRefresh, t0+2*dailySec)
	}
	// Weekly window did NOT roll (25h < 7d); usage persists and next_refresh is
	// still the original window_start (t0) + 7d.
	if progress.WeeklyUsed.Used != 2.5 {
		t.Fatalf("weekly used = %v, want 2.5 (not rolled)", progress.WeeklyUsed.Used)
	}
	weeklySec := int64(quotaWeeklyWindow.Seconds())
	if progress.WeeklyUsed.NextRefresh != t0+weeklySec {
		t.Fatalf("weekly next_refresh = %d, want %d", progress.WeeklyUsed.NextRefresh, t0+weeklySec)
	}
}

// M2: an active subscription renewed in place is recorded as renewal_strategy
// "extend"; a grant with no active subscription is recorded as "new". The
// column makes the "expired but not revoked" policy explicit and observable.
func TestAssignOrExtend_RecordsRenewalStrategyExtend(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	first, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 9000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("initial assign: %v", err)
	}
	if first.RenewalStrategy != RenewalStrategyNew {
		t.Fatalf("initial grant renewal_strategy = %q, want %q", first.RenewalStrategy, RenewalStrategyNew)
	}

	renewed, reused, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 5000, ExpiresAt: 5000 + 30*86400, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !reused {
		t.Fatal("renewal should reuse the active subscription")
	}
	if renewed.RenewalStrategy != RenewalStrategyExtend {
		t.Fatalf("renewal renewal_strategy = %q, want %q", renewed.RenewalStrategy, RenewalStrategyExtend)
	}
	// The strategy must have been persisted via the narrow-field update.
	stored, err := uc.GetActiveSubscriptionForUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.RenewalStrategy != RenewalStrategyExtend {
		t.Fatalf("persisted renewal_strategy = %q, want %q", stored.RenewalStrategy, RenewalStrategyExtend)
	}
}

func TestAssign_RecordsRenewalStrategyNew(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	sub, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 5000, ExpiresAt: 5000 + 30*86400, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if sub.RenewalStrategy != RenewalStrategyNew {
		t.Fatalf("assign renewal_strategy = %q, want %q", sub.RenewalStrategy, RenewalStrategyNew)
	}
}

// TestExtend_RecordsRenewalStrategyExtend (M2) closes the gap that the Extend
// usecase — reachable via admin ExtendSubscription — previously did not write
// renewal_strategy, leaving an admin-extended row stuck on its initial
// 'new'/” value and breaking the "expired but not revoked" observability. It
// also pins the domain-H1 narrow write: Extend must not clobber usage columns
// from the read snapshot.
func TestExtend_RecordsRenewalStrategyExtend(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	// A fresh grant starts as 'new' with no usage.
	sub, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 5000, ExpiresAt: 5000 + 30*86400, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if sub.RenewalStrategy != RenewalStrategyNew {
		t.Fatalf("initial grant renewal_strategy = %q, want %q", sub.RenewalStrategy, RenewalStrategyNew)
	}

	// Simulate a concurrent AddUsage increment landing before the Extend.
	sub.DailyUsageUSD = 1.25
	sub.WeeklyUsageUSD = 1.25
	sub.MonthlyUsageUSD = 1.25
	if err := repo.UpdateSubscriptionFields(context.Background(), sub, []SubscriptionField{SubscriptionFieldUsageAll}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	// Extend the expiry; it must flip the strategy to 'extend' and persist via
	// the narrow write (expires_at + renewal_strategy only).
	if err := uc.Extend(context.Background(), sub.ID, 5000+60*86400); err != nil {
		t.Fatalf("extend: %v", err)
	}
	stored, err := uc.GetActiveSubscriptionForUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.RenewalStrategy != RenewalStrategyExtend {
		t.Fatalf("extend renewal_strategy = %q, want %q", stored.RenewalStrategy, RenewalStrategyExtend)
	}
	if stored.ExpiresAt != 5000+60*86400 {
		t.Fatalf("extend expires_at = %d, want %d", stored.ExpiresAt, 5000+60*86400)
	}
	// domain-H1: the narrow write must NOT have clobbered the seeded usage.
	if stored.DailyUsageUSD != 1.25 || stored.WeeklyUsageUSD != 1.25 || stored.MonthlyUsageUSD != 1.25 {
		t.Fatalf("extend clobbered usage: daily=%v weekly=%v monthly=%v, want 1.25 each",
			stored.DailyUsageUSD, stored.WeeklyUsageUSD, stored.MonthlyUsageUSD)
	}
}

// TestExtend_ReactivatesExpiredSubscription pins the Extend reactivation
// semantic: when the hourly checker has already flipped a subscription to
// expired, an admin extension must flip it back to active (and record
// 'extend') — otherwise the active read path (status='active' AND
// expires_at > now) keeps the user without usable entitlement despite the
// fresh expiry.
func TestExtend_ReactivatesExpiredSubscription(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	sub, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 4000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}

	// The hourly checker marks the now-expired subscription as expired.
	sub.Status = SubscriptionStatusExpired
	if err := repo.UpdateSubscriptionFields(context.Background(), sub, []SubscriptionField{SubscriptionFieldStatus}); err != nil {
		t.Fatalf("mark expired: %v", err)
	}
	if _, err := uc.GetActiveSubscriptionForUser(context.Background(), 1); err == nil {
		t.Fatal("expired subscription must not be active")
	}

	// Admin extends it well into the future.
	if err := uc.Extend(context.Background(), sub.ID, 5000+60*86400); err != nil {
		t.Fatalf("extend: %v", err)
	}

	active, err := uc.GetActiveSubscriptionForUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("extended subscription must be active again: %v", err)
	}
	if active.Status != SubscriptionStatusActive {
		t.Fatalf("status = %q, want active", active.Status)
	}
	if active.RenewalStrategy != RenewalStrategyExtend {
		t.Fatalf("renewal_strategy = %q, want %q", active.RenewalStrategy, RenewalStrategyExtend)
	}
	if active.ExpiresAt != 5000+60*86400 {
		t.Fatalf("expires_at = %d, want %d", active.ExpiresAt, 5000+60*86400)
	}
}

// --- P1.2: concurrent active subscription race regression tests ---

// mockConcurrentCreateRepo wraps mockSubscriptionRepo to simulate the DB-level
// unique-index collision that happens when two concurrent Assign/AssignOrExtend
// calls both pass the "no active subscription" pre-check and race into
// CreateSubscription. The first Create for a given user succeeds; a concurrent
// second Create returns ErrSubscriptionAlreadyAssigned, exactly as the real
// repo does when the H10 unique index rejects the duplicate insert.
type mockConcurrentCreateRepo struct {
	*mockSubscriptionRepo
	// mu serialises CreateSubscription so the simulated collision is
	// deterministic rather than racy.
	mu             sync.Mutex
	createdActive  map[int64]bool
	createErrOnDup bool
}

func newMockConcurrentCreateRepo() *mockConcurrentCreateRepo {
	return &mockConcurrentCreateRepo{
		mockSubscriptionRepo: newMockSubscriptionRepo(),
		createdActive:        map[int64]bool{},
		createErrOnDup:       true,
	}
}

func (m *mockConcurrentCreateRepo) CreateSubscription(ctx context.Context, sub *UserSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub.Status == SubscriptionStatusActive && m.createdActive[sub.UserID] && m.createErrOnDup {
		return ErrSubscriptionAlreadyAssigned
	}
	if err := m.mockSubscriptionRepo.CreateSubscription(ctx, sub); err != nil {
		return err
	}
	if sub.Status == SubscriptionStatusActive {
		m.createdActive[sub.UserID] = true
	}
	return nil
}

func (m *mockConcurrentCreateRepo) CreateSubscriptionInTx(ctx context.Context, tx Tx, sub *UserSubscription) error {
	return m.CreateSubscription(ctx, sub)
}

// TestAssign_PropagatesDuplicateKeyFromDB simulates the concurrent race: both
// goroutines pass the GetActiveSubscriptionByUser pre-check (returns nil), then
// the DB unique index rejects the second insert. The usecase must propagate
// ErrSubscriptionAlreadyAssigned to at least one caller rather than creating
// two active subscriptions.
func TestAssign_PropagatesDuplicateKeyFromDB(t *testing.T) {
	repo := newMockConcurrentCreateRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(1000, 0) }

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{
				UserID: 9001, GroupID: group.ID, ExpiresAt: 2000,
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	var successes, alreadyAssigned, otherErrs int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSubscriptionAlreadyAssigned):
			alreadyAssigned++
		default:
			otherErrs++
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if alreadyAssigned != 1 {
		t.Fatalf("alreadyAssigned = %d, want 1 (the racing Assign must get ErrSubscriptionAlreadyAssigned from the DB index)", alreadyAssigned)
	}
	if otherErrs != 0 {
		t.Fatalf("otherErrs = %d, want 0", otherErrs)
	}
}

// mockRaceWindowRepo simulates the exact concurrent race window: both goroutines
// pass GetActiveSubscriptionByUser (returns nil) before either calls
// CreateSubscription. The first Create succeeds; the second returns
// ErrSubscriptionAlreadyAssigned exactly as the H10 DB unique index would.
type mockRaceWindowRepo struct {
	*mockSubscriptionRepo
	createErr error // if non-nil, CreateSubscription returns this
}

func (m *mockRaceWindowRepo) CreateSubscription(ctx context.Context, sub *UserSubscription) error {
	if m.createErr != nil {
		return m.createErr
	}
	return m.mockSubscriptionRepo.CreateSubscription(ctx, sub)
}

func (m *mockRaceWindowRepo) CreateSubscriptionInTx(ctx context.Context, tx Tx, sub *UserSubscription) error {
	return m.CreateSubscription(ctx, sub)
}

// TestAssignOrExtend_PropagatesDuplicateKeyFromDB simulates the DB-level
// collision inside the concurrent race window: GetActiveSubscriptionByUser
// returns nil (both callers raced past the pre-check), then CreateSubscription
// returns ErrSubscriptionAlreadyAssigned (the H10 unique index rejected the
// duplicate insert). AssignOrExtend must propagate this error, not swallow it.
func TestAssignOrExtend_PropagatesDuplicateKeyFromDB(t *testing.T) {
	repo := &mockRaceWindowRepo{
		mockSubscriptionRepo: newMockSubscriptionRepo(),
		createErr:            ErrSubscriptionAlreadyAssigned,
	}
	group := &SubscriptionGroup{Name: "team", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(1000, 0) }

	_, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 9002, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 2000,
	})
	if !errors.Is(err, ErrSubscriptionAlreadyAssigned) {
		t.Fatalf("AssignOrExtend() error = %v, want ErrSubscriptionAlreadyAssigned (DB unique index collision must propagate)", err)
	}
}
