package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/monitor/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

func TestIAMB4MonitorOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres", "memory"} {
		t.Run(driver, func(t *testing.T) {
			r := newMemoryRepository()
			if driver != "memory" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			ctx := context.Background()
			one := &biz.AlertRule{Name: "one", ServiceName: "one", Metric: "latency", CreatedAt: time.Now()}
			two := &biz.AlertRule{Name: "two", ServiceName: "two", Metric: "latency", CreatedAt: time.Now()}
			require.NoError(t, r.CreateAlertRule(ctx, one))
			require.NoError(t, r.CreateAlertRule(ctx, two))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"monitor.alert_rule.list": authztest.Resources(one.ID), "monitor.alert_rule.read": authztest.Resources(one.ID), "monitor.alert_rule.update": authztest.Resources(one.ID), "monitor.alert_rule.delete": authztest.Resources(one.ID)}}
			uc := biz.NewMonitorUsecase(r)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			rows, total, err := uc.ListAlertRules(request, 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			_, err = uc.GetAlertRule(request, two.ID)
			require.Error(t, err)
			require.Error(t, uc.UpdateAlertRule(request, two))
			require.Error(t, uc.DeleteAlertRule(request, two.ID))
			require.NoError(t, uc.DeleteAlertRule(request, one.ID))
			check := &biz.HealthCheck{ServiceName: "worker", Status: "healthy", CheckedAt: time.Now()}
			require.NoError(t, r.SaveHealthCheck(ctx, check))
			policy.Scopes["monitor.health.service.read"] = authztest.Resources(check.ID)
			_, err = uc.GetLatestHealth(request, "worker")
			require.NoError(t, err)
			newer := &biz.HealthCheck{ServiceName: "worker", Status: "unhealthy", CheckedAt: time.Now().Add(time.Second)}
			require.NoError(t, r.SaveHealthCheck(ctx, newer))
			_, err = uc.GetLatestHealth(request, "worker")
			require.Error(t, err) // never fall back to an older allowed row
			require.Error(t, uc.RecordHealthCheck(request, "forged", "healthy", 1))
		})
	}
}
