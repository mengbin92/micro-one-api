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
			request := authorization.WithExpectedResourceRevision(authorization.WithWriteReason(authztest.Context(), "monitor owner acceptance"), one.Revision)
			rows, total, err := uc.ListAlertRules(request, 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			_, err = uc.GetAlertRule(request, two.ID)
			require.Error(t, err)
			require.Error(t, uc.UpdateAlertRule(request, two))
			require.Error(t, uc.DeleteAlertRule(request, two.ID))
			if driver == "memory" {
				require.ErrorIs(t, uc.DeleteAlertRule(request, one.ID), authorization.ErrWriteStorageUnavailable)
			} else {
				require.NoError(t, uc.DeleteAlertRule(request, one.ID))
			}
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

func TestIAMB4MonitorCASAuditDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, driver)}
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"monitor.alert_rule.create": authztest.All(), "monitor.alert_rule.update": authztest.All(), "monitor.alert_rule.delete": authztest.All()}}
			uc := biz.NewMonitorUsecase(r)
			uc.SetAuthorization(policy)
			request := authorization.WithExpectedResourceRevision(authorization.WithWriteReason(authztest.Context(), "CAS acceptance"), 0)
			rule := &biz.AlertRule{Name: "initial", ServiceName: "relay", Metric: "latency"}
			require.NoError(t, uc.CreateAlertRule(request, rule))
			require.EqualValues(t, 1, rule.Revision)
			rule.Name = "current"
			require.ErrorIs(t, uc.UpdateAlertRule(request, rule), biz.ErrAlertRuleRevisionConflict)
			request = authorization.WithExpectedResourceRevision(request, 1)
			require.NoError(t, uc.UpdateAlertRule(request, rule))
			current, err := r.GetAlertRule(context.Background(), rule.ID)
			require.NoError(t, err)
			require.EqualValues(t, 2, current.Revision)
			require.ErrorIs(t, uc.DeleteAlertRule(request, rule.ID), biz.ErrAlertRuleRevisionConflict)
			require.NoError(t, r.db.Exec("DROP TABLE resource_write_audits").Error)
			request = authorization.WithExpectedResourceRevision(request, 2)
			require.Error(t, uc.DeleteAlertRule(request, rule.ID))
			stored, err := r.GetAlertRule(context.Background(), rule.ID)
			require.NoError(t, err)
			require.Equal(t, "current", stored.Name)
			require.EqualValues(t, 2, stored.Revision)
		})
	}
}
