package integration

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	monitorv1 "micro-one-api/api/monitor/v1"
	monitorbiz "micro-one-api/app/monitor/internal/biz"
	monitorservice "micro-one-api/app/monitor/internal/service"
)

// ========== Monitor Service Tests ==========

type testMonitorRepo struct {
	checks []*monitorbiz.HealthCheck
	rules  []*monitorbiz.AlertRule
	idSeq  int64
}

func (r *testMonitorRepo) SaveHealthCheck(ctx context.Context, check *monitorbiz.HealthCheck) error {
	r.idSeq++
	check.ID = r.idSeq
	r.checks = append(r.checks, check)
	return nil
}

func (r *testMonitorRepo) ListHealthChecks(ctx context.Context, serviceName string, page, pageSize int32) ([]*monitorbiz.HealthCheck, int64, error) {
	var result []*monitorbiz.HealthCheck
	for _, c := range r.checks {
		if serviceName == "" || c.ServiceName == serviceName {
			result = append(result, c)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testMonitorRepo) GetLatestHealthCheck(ctx context.Context, serviceName string) (*monitorbiz.HealthCheck, error) {
	for i := len(r.checks) - 1; i >= 0; i-- {
		if r.checks[i].ServiceName == serviceName {
			return r.checks[i], nil
		}
	}
	return nil, monitorbiz.ErrHealthCheckNotFound
}

func (r *testMonitorRepo) CreateAlertRule(ctx context.Context, rule *monitorbiz.AlertRule) error {
	r.idSeq++
	rule.ID = r.idSeq
	r.rules = append(r.rules, rule)
	return nil
}

func (r *testMonitorRepo) GetAlertRule(ctx context.Context, id int64) (*monitorbiz.AlertRule, error) {
	for _, rule := range r.rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return nil, monitorbiz.ErrAlertRuleNotFound
}

func (r *testMonitorRepo) ListAlertRules(ctx context.Context, page, pageSize int32) ([]*monitorbiz.AlertRule, int64, error) {
	return r.rules, int64(len(r.rules)), nil
}

func (r *testMonitorRepo) UpdateAlertRule(ctx context.Context, rule *monitorbiz.AlertRule) error {
	for i, existing := range r.rules {
		if existing.ID == rule.ID {
			r.rules[i] = rule
			return nil
		}
	}
	return monitorbiz.ErrAlertRuleNotFound
}

func (r *testMonitorRepo) DeleteAlertRule(ctx context.Context, id int64) error {
	for i, rule := range r.rules {
		if rule.ID == id {
			r.rules = append(r.rules[:i], r.rules[i+1:]...)
			return nil
		}
	}
	return monitorbiz.ErrAlertRuleNotFound
}

func setupMonitorService(t *testing.T, addr string) (func(), monitorv1.MonitorServiceClient) {
	repo := &testMonitorRepo{}
	uc := monitorbiz.NewMonitorUsecase(repo)
	svc := monitorservice.NewMonitorService(uc)

	server := grpc.NewServer()
	monitorv1.RegisterMonitorServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("monitor server error: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
		lis.Close()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	return cleanup, monitorv1.NewMonitorServiceClient(conn)
}

func TestMonitorIntegration(t *testing.T) {
	cleanup, client := setupMonitorService(t, "127.0.0.1:19012")
	defer cleanup()

	ctx := context.Background()

	t.Run("SaveAndListHealthChecks", func(t *testing.T) {
		_, err := client.SaveHealthCheck(ctx, &monitorv1.SaveHealthCheckRequest{
			ServiceName:  "relay-gateway",
			Status:       "healthy",
			ResponseTime: 50,
		})
		if err != nil {
			t.Fatalf("SaveHealthCheck failed: %v", err)
		}

		resp, err := client.ListHealthChecks(ctx, &monitorv1.ListHealthChecksRequest{
			ServiceName: "relay-gateway",
			Page:        1,
			PageSize:    10,
		})
		if err != nil {
			t.Fatalf("ListHealthChecks failed: %v", err)
		}
		if resp.Total < 1 {
			t.Fatalf("expected at least 1 health check, got %d", resp.Total)
		}
	})

	t.Run("CreateAndListAlertRules", func(t *testing.T) {
		_, err := client.CreateAlertRule(ctx, &monitorv1.CreateAlertRuleRequest{
			Name:        "high-latency",
			ServiceName: "relay-gateway",
			Metric:      "response_time",
			Threshold:   1000,
			Operator:    ">",
			Duration:    60,
			Enabled:     true,
		})
		if err != nil {
			t.Fatalf("CreateAlertRule failed: %v", err)
		}

		resp, err := client.ListAlertRules(ctx, &monitorv1.ListAlertRulesRequest{
			Page:     1,
			PageSize: 10,
		})
		if err != nil {
			t.Fatalf("ListAlertRules failed: %v", err)
		}
		if resp.Total < 1 {
			t.Fatalf("expected at least 1 alert rule, got %d", resp.Total)
		}
	})
}
