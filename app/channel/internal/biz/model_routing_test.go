package biz

import (
	"context"
	"testing"
	"time"
)

// mockModelRoutingRepo is a minimal in-memory ModelRoutingRepo for tests.
type mockModelRoutingRepo struct {
	rows []*ModelRouting
}

func (m *mockModelRoutingRepo) ListModelRoutings(_ context.Context, group, model, platform string) ([]*ModelRouting, error) {
	out := make([]*ModelRouting, 0)
	for _, r := range m.rows {
		if group != "" && r.GroupName != group {
			continue
		}
		if model != "" && r.Model != model {
			continue
		}
		if platform != "" && r.Platform != platform {
			continue
		}
		clone := *r
		out = append(out, &clone)
	}
	return out, nil
}

func (m *mockModelRoutingRepo) ListModelRoutingsForSelect(_ context.Context, group, _, _ string) ([]*ModelRouting, error) {
	// Return all enabled rows for the group; the biz helper applies precedence.
	out := make([]*ModelRouting, 0)
	for _, r := range m.rows {
		if r.GroupName != group || !r.Enabled {
			continue
		}
		clone := *r
		out = append(out, &clone)
	}
	return out, nil
}

func (m *mockModelRoutingRepo) UpsertModelRouting(_ context.Context, r *ModelRouting) error {
	for i, row := range m.rows {
		if row.GroupName == r.GroupName && row.Model == r.Model &&
			row.Platform == r.Platform && row.SubscriptionAccountID == r.SubscriptionAccountID {
			m.rows[i] = r
			return nil
		}
	}
	m.rows = append(m.rows, r)
	return nil
}

func (m *mockModelRoutingRepo) DeleteModelRouting(_ context.Context, id int64) error {
	for i, row := range m.rows {
		if row.ID == id {
			m.rows = append(m.rows[:i], m.rows[i+1:]...)
			return nil
		}
	}
	return ErrModelRoutingNotFound
}

func TestRoutingMatchForSelect_ExactBeforeWildcard(t *testing.T) {
	rows := []*ModelRouting{
		{ID: 1, Model: "claude-*", SubscriptionAccountID: 10, Enabled: true},
		{ID: 2, Model: "claude-sonnet-4", SubscriptionAccountID: 20, Enabled: true},
		{ID: 3, Model: "*", SubscriptionAccountID: 30, Enabled: true},
	}
	matches := RoutingMatchForSelect(rows, "claude-sonnet-4")
	if len(matches) != 1 || matches[0].SubscriptionAccountID != 20 {
		t.Fatalf("exact match must win: got %+v", matches)
	}
}

func TestRoutingMatchForSelect_SpecificWildcardBeforeCatchAll(t *testing.T) {
	rows := []*ModelRouting{
		{ID: 1, Model: "*", SubscriptionAccountID: 30, Enabled: true},
		{ID: 2, Model: "claude-*", SubscriptionAccountID: 10, Enabled: true},
	}
	matches := RoutingMatchForSelect(rows, "claude-sonnet-4")
	if len(matches) != 1 || matches[0].SubscriptionAccountID != 10 {
		t.Fatalf("specific wildcard must win over catch-all: got %+v", matches)
	}
}

func TestRoutingMatchForSelect_CatchAllFallback(t *testing.T) {
	rows := []*ModelRouting{
		{ID: 1, Model: "*", SubscriptionAccountID: 30, Enabled: true},
	}
	matches := RoutingMatchForSelect(rows, "gpt-4o")
	if len(matches) != 1 || matches[0].SubscriptionAccountID != 30 {
		t.Fatalf("catch-all must match any: got %+v", matches)
	}
}

func TestRoutingMatchForSelect_NoMatch(t *testing.T) {
	rows := []*ModelRouting{
		{ID: 1, Model: "claude-*", SubscriptionAccountID: 10, Enabled: true},
	}
	matches := RoutingMatchForSelect(rows, "gpt-4o")
	if matches != nil {
		t.Fatalf("non-matching pattern must return nil: got %+v", matches)
	}
}

func TestSelectSubscriptionAccount_RoutingPinsAccount(t *testing.T) {
	now := time.Unix(1710000000, 0)
	repo := &mockChannelRepo{
		accounts: map[int64]*SubscriptionAccount{
			1: {ID: 1, Name: "a", Status: ChannelStatusEnabled, Platform: "codex", Priority: 10},
			2: {ID: 2, Name: "b", Status: ChannelStatusEnabled, Platform: "codex", Priority: 10},
		},
		accAbilities: map[string][]SubscriptionAccountAbility{
			"codex:default:gpt-5": {
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 2, Enabled: true, Priority: 10},
			},
		},
	}
	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }
	uc.SetModelRoutingRepo(&mockModelRoutingRepo{
		rows: []*ModelRouting{
			{ID: 1, GroupName: "default", Model: "gpt-5", SubscriptionAccountID: 2, Enabled: true, Priority: 0},
		},
	})

	account, err := uc.SelectSubscriptionAccount(context.Background(), "default", "gpt-5", "codex", false)
	if err != nil {
		t.Fatalf("SelectSubscriptionAccount() error = %v", err)
	}
	if account.ID != 2 {
		t.Fatalf("routing must pin to account 2, got %d", account.ID)
	}
}

func TestSelectSubscriptionAccount_NoRoutingUsesNormalSelection(t *testing.T) {
	now := time.Unix(1710000000, 0)
	repo := &mockChannelRepo{
		accounts: map[int64]*SubscriptionAccount{
			1: {ID: 1, Name: "a", Status: ChannelStatusEnabled, Platform: "codex", Priority: 10},
			2: {ID: 2, Name: "b", Status: ChannelStatusEnabled, Platform: "codex", Priority: 10},
		},
		accAbilities: map[string][]SubscriptionAccountAbility{
			"codex:default:gpt-5": {
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 2, Enabled: true, Priority: 10},
			},
		},
	}
	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }
	// No routing repo set → normal selection.

	account, err := uc.SelectSubscriptionAccount(context.Background(), "default", "gpt-5", "codex", false)
	if err != nil {
		t.Fatalf("SelectSubscriptionAccount() error = %v", err)
	}
	if account.ID != 1 && account.ID != 2 {
		t.Fatalf("expected account 1 or 2, got %d", account.ID)
	}
}

func TestSubscriptionAccountSelector_FailingAccountDerated(t *testing.T) {
	sel := NewSubscriptionAccountSelector()
	// Account 1: healthy.
	// Account 2: record many failures to derate its health factor.
	for i := 0; i < 100; i++ {
		sel.RecordAccountHealth(2, false)
	}
	tier := []*SubscriptionAccount{
		{ID: 1, Priority: 10},
		{ID: 2, Priority: 10},
	}
	// Run several selections; account 1 (healthy) should win the vast majority.
	healthyPicks := 0
	for i := 0; i < 50; i++ {
		acct, err := sel.Select(context.Background(), "default", tier)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if acct.ID == 1 {
			healthyPicks++
		}
	}
	if healthyPicks < 45 {
		t.Fatalf("healthy account should dominate, got %d/50 healthy picks", healthyPicks)
	}
}

func TestSubscriptionAccountSelector_CircuitOpenExcludesAccount(t *testing.T) {
	sel := NewSubscriptionAccountSelector()
	// Trip account 2's circuit breaker (>0.5 errors/sec).
	for i := 0; i < 100; i++ {
		sel.RecordAccountHealth(2, false)
	}
	tier := []*SubscriptionAccount{
		{ID: 1, Priority: 10},
		{ID: 2, Priority: 10},
	}
	for i := 0; i < 10; i++ {
		acct, err := sel.Select(context.Background(), "default", tier)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if acct.ID == 2 {
			t.Fatalf("circuit-open account 2 must never be selected")
		}
	}
}

func TestSubscriptionAccountSelector_AcquireRelease(t *testing.T) {
	sel := NewSubscriptionAccountSelector()
	sel.Acquire(1)
	sel.Acquire(1)
	stats := sel.GetStats()
	if stats[1].Inflight != 2 {
		t.Fatalf("inflight = %d, want 2", stats[1].Inflight)
	}
	sel.Release(1)
	stats = sel.GetStats()
	if stats[1].Inflight != 1 {
		t.Fatalf("inflight after release = %d, want 1", stats[1].Inflight)
	}
	// Release below zero is a no-op.
	sel.Release(1)
	sel.Release(1)
	stats = sel.GetStats()
	if stats[1].Inflight != 0 {
		t.Fatalf("inflight after over-release = %d, want 0", stats[1].Inflight)
	}
}

func TestSubscriptionAccountSelector_EmptyTier(t *testing.T) {
	sel := NewSubscriptionAccountSelector()
	_, err := sel.Select(context.Background(), "default", nil)
	if err != ErrSubscriptionAccountNotFound {
		t.Fatalf("empty tier must return ErrSubscriptionAccountNotFound, got %v", err)
	}
}

