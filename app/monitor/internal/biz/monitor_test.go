package biz

import (
	"context"
	"testing"
	"time"
)

type mockMonitorRepo struct {
	checks map[int64]*HealthCheck
	rules  map[int64]*AlertRule
	seq    int64
}

func (m *mockMonitorRepo) SaveHealthCheck(ctx context.Context, check *HealthCheck) error {
	m.seq++
	check.ID = m.seq
	m.checks[check.ID] = check
	return nil
}

func (m *mockMonitorRepo) ListHealthChecks(ctx context.Context, serviceName string, page, pageSize int32) ([]*HealthCheck, int64, error) {
	var result []*HealthCheck
	for _, c := range m.checks {
		if serviceName == "" || c.ServiceName == serviceName {
			result = append(result, c)
		}
	}
	total := int64(len(result))
	start := int((page - 1) * pageSize)
	if start >= len(result) {
		return nil, total, nil
	}
	end := min(start+int(pageSize), len(result))
	return result[start:end], total, nil
}

func (m *mockMonitorRepo) GetLatestHealthCheck(ctx context.Context, serviceName string) (*HealthCheck, error) {
	var latest *HealthCheck
	for _, c := range m.checks {
		if c.ServiceName == serviceName {
			if latest == nil || c.ID > latest.ID {
				latest = c
			}
		}
	}
	if latest == nil {
		return nil, ErrHealthCheckNotFound
	}
	return latest, nil
}

func (m *mockMonitorRepo) CreateAlertRule(ctx context.Context, rule *AlertRule) error {
	m.seq++
	rule.ID = m.seq
	m.rules[rule.ID] = rule
	return nil
}

func (m *mockMonitorRepo) GetAlertRule(ctx context.Context, id int64) (*AlertRule, error) {
	r, ok := m.rules[id]
	if !ok {
		return nil, ErrAlertRuleNotFound
	}
	return r, nil
}

func (m *mockMonitorRepo) ListAlertRules(ctx context.Context, page, pageSize int32) ([]*AlertRule, int64, error) {
	var result []*AlertRule
	for _, r := range m.rules {
		result = append(result, r)
	}
	total := int64(len(result))
	start := int((page - 1) * pageSize)
	if start >= len(result) {
		return nil, total, nil
	}
	end := min(start+int(pageSize), len(result))
	return result[start:end], total, nil
}

func (m *mockMonitorRepo) UpdateAlertRule(ctx context.Context, rule *AlertRule) error {
	if _, ok := m.rules[rule.ID]; !ok {
		return ErrAlertRuleNotFound
	}
	m.rules[rule.ID] = rule
	return nil
}

func (m *mockMonitorRepo) DeleteAlertRule(ctx context.Context, id int64) error {
	if _, ok := m.rules[id]; !ok {
		return ErrAlertRuleNotFound
	}
	delete(m.rules, id)
	return nil
}

func newMockMonitorRepo() *mockMonitorRepo {
	return &mockMonitorRepo{
		checks: make(map[int64]*HealthCheck),
		rules:  make(map[int64]*AlertRule),
	}
}

func TestMonitorUsecase_RecordHealthCheck(t *testing.T) {
	repo := newMockMonitorRepo()
	uc := NewMonitorUsecase(repo)

	err := uc.RecordHealthCheck(context.Background(), "relay-gateway", HealthStatusHealthy, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks, total, _ := uc.ListHealthChecks(context.Background(), "relay-gateway", 1, 20)
	if total != 1 {
		t.Fatalf("expected 1, got %d", total)
	}
	if checks[0].Status != HealthStatusHealthy {
		t.Fatalf("expected healthy, got %s", checks[0].Status)
	}
	if checks[0].ResponseTime != 42 {
		t.Fatalf("expected 42ms, got %d", checks[0].ResponseTime)
	}
	if checks[0].CheckedAt.IsZero() {
		t.Fatal("expected CheckedAt to be set")
	}
}

func TestMonitorUsecase_GetLatestHealth(t *testing.T) {
	repo := newMockMonitorRepo()
	uc := NewMonitorUsecase(repo)

	uc.RecordHealthCheck(context.Background(), "svc", HealthStatusHealthy, 10)
	uc.RecordHealthCheck(context.Background(), "svc", HealthStatusUnhealthy, 500)

	latest, err := uc.GetLatestHealth(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if latest.Status != HealthStatusUnhealthy {
		t.Fatalf("expected unhealthy, got %s", latest.Status)
	}

	_, err = uc.GetLatestHealth(context.Background(), "nonexistent")
	if err != ErrHealthCheckNotFound {
		t.Fatalf("expected ErrHealthCheckNotFound, got %v", err)
	}
}

func TestMonitorUsecase_CreateAlertRule(t *testing.T) {
	repo := newMockMonitorRepo()
	uc := NewMonitorUsecase(repo)

	t.Run("success", func(t *testing.T) {
		rule := &AlertRule{
			Name: "high-latency", ServiceName: "relay-gateway",
			Metric: "response_time", Threshold: 500, Operator: "gt", Duration: 60, Enabled: true,
		}
		err := uc.CreateAlertRule(context.Background(), rule)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule.ID == 0 {
			t.Fatal("expected ID to be set")
		}
		if rule.CreatedAt.IsZero() {
			t.Fatal("expected CreatedAt to be set")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		rule := &AlertRule{ServiceName: "svc", Metric: "cpu"}
		err := uc.CreateAlertRule(context.Background(), rule)
		if err != ErrInvalidAlertRule {
			t.Fatalf("expected ErrInvalidAlertRule, got %v", err)
		}
	})

	t.Run("missing service name", func(t *testing.T) {
		rule := &AlertRule{Name: "r", Metric: "cpu"}
		err := uc.CreateAlertRule(context.Background(), rule)
		if err != ErrInvalidAlertRule {
			t.Fatalf("expected ErrInvalidAlertRule, got %v", err)
		}
	})

	t.Run("missing metric", func(t *testing.T) {
		rule := &AlertRule{Name: "r", ServiceName: "svc"}
		err := uc.CreateAlertRule(context.Background(), rule)
		if err != ErrInvalidAlertRule {
			t.Fatalf("expected ErrInvalidAlertRule, got %v", err)
		}
	})
}

func TestMonitorUsecase_AlertRuleCRUD(t *testing.T) {
	repo := newMockMonitorRepo()
	uc := NewMonitorUsecase(repo)

	rule := &AlertRule{
		Name: "cpu-high", ServiceName: "relay-gateway",
		Metric: "cpu", Threshold: 80, Operator: "gt", Duration: 30, Enabled: true,
	}
	uc.CreateAlertRule(context.Background(), rule)

	t.Run("get", func(t *testing.T) {
		got, err := uc.GetAlertRule(context.Background(), rule.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Name != "cpu-high" {
			t.Fatalf("expected cpu-high, got %s", got.Name)
		}
	})

	t.Run("list", func(t *testing.T) {
		rules, total, err := uc.ListAlertRules(context.Background(), 1, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(rules))
		}
	})

	t.Run("update", func(t *testing.T) {
		rule.Threshold = 90
		err := uc.UpdateAlertRule(context.Background(), rule)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := uc.GetAlertRule(context.Background(), rule.ID)
		if got.Threshold != 90 {
			t.Fatalf("expected 90, got %f", got.Threshold)
		}
	})

	t.Run("delete", func(t *testing.T) {
		err := uc.DeleteAlertRule(context.Background(), rule.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = uc.GetAlertRule(context.Background(), rule.ID)
		if err != ErrAlertRuleNotFound {
			t.Fatalf("expected ErrAlertRuleNotFound, got %v", err)
		}
	})

	t.Run("get nonexistent", func(t *testing.T) {
		_, err := uc.GetAlertRule(context.Background(), 999)
		if err != ErrAlertRuleNotFound {
			t.Fatalf("expected ErrAlertRuleNotFound, got %v", err)
		}
	})

	t.Run("update nonexistent", func(t *testing.T) {
		err := uc.UpdateAlertRule(context.Background(), &AlertRule{ID: 999})
		if err != ErrAlertRuleNotFound {
			t.Fatalf("expected ErrAlertRuleNotFound, got %v", err)
		}
	})

	t.Run("delete nonexistent", func(t *testing.T) {
		err := uc.DeleteAlertRule(context.Background(), 999)
		if err != ErrAlertRuleNotFound {
			t.Fatalf("expected ErrAlertRuleNotFound, got %v", err)
		}
	})
}

func TestMonitorUsecase_ListPagination(t *testing.T) {
	repo := newMockMonitorRepo()
	uc := NewMonitorUsecase(repo)

	for i := range 5 {
		uc.RecordHealthCheck(context.Background(), "svc", HealthStatusHealthy, int64(i*10))
	}

	_, total, _ := uc.ListHealthChecks(context.Background(), "svc", 1, 20)
	if total != 5 {
		t.Fatalf("expected 5, got %d", total)
	}

	// normalizes page < 1
	_, total, _ = uc.ListHealthChecks(context.Background(), "svc", 0, 20)
	if total != 5 {
		t.Fatalf("expected 5, got %d", total)
	}

	// normalizes pageSize
	_, _, err := uc.ListHealthChecks(context.Background(), "svc", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealthStatus_Constants(t *testing.T) {
	if HealthStatusHealthy != "healthy" {
		t.Fatalf("expected healthy, got %s", HealthStatusHealthy)
	}
	if HealthStatusUnhealthy != "unhealthy" {
		t.Fatalf("expected unhealthy, got %s", HealthStatusUnhealthy)
	}
	if HealthStatusUnknown != "unknown" {
		t.Fatalf("expected unknown, got %s", HealthStatusUnknown)
	}
}

func TestAlertRule_Fields(t *testing.T) {
	now := time.Now()
	r := &AlertRule{
		ID: 1, Name: "n", ServiceName: "s", Metric: "m",
		Threshold: 1.5, Operator: "gt", Duration: 60, Enabled: true, CreatedAt: now,
	}
	if r.ID != 1 || r.Name != "n" || r.ServiceName != "s" || r.Metric != "m" {
		t.Fatalf("unexpected fields: %+v", r)
	}
	if r.Threshold != 1.5 || r.Operator != "gt" || r.Duration != 60 || !r.Enabled {
		t.Fatalf("unexpected fields: %+v", r)
	}
}
