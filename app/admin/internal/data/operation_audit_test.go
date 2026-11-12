package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	adminbiz "micro-one-api/app/admin/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestOperationAuditRepoFiltersOrdersAndPaginates(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			for _, row := range []map[string]any{
				{"event_id": "a1", "actor_user_id": 1, "operation": "billing.pricing.update", "resource_id": "100", "decision_versions": `{"policy":"1"}`, "reason": "first", "occurred_at": 1000, "result": "success"},
				{"event_id": "a2", "actor_user_id": 1, "operation": "billing.pricing.update", "resource_id": "101", "decision_versions": `{"policy":"2"}`, "reason": "second", "occurred_at": 2000, "result": "failure"},
				{"event_id": "a3", "actor_user_id": 2, "operation": "channel.channel.update", "resource_id": "100", "decision_versions": `{}`, "reason": "third", "occurred_at": 3000, "result": "success"},
				{"event_id": "a4", "actor_user_id": 3, "operation": "billing.upstream_cost.update", "resource_id": "200", "decision_versions": `{}`, "reason": "fourth", "occurred_at": 4000, "result": "success"},
			} {
				require.NoError(t, db.Table("resource_write_audits").Create(row).Error)
			}
			sqlDB, err := db.DB()
			require.NoError(t, err)
			uc := adminbiz.NewOperationAuditUsecase(NewOperationAuditRepoWithDriver(sqlDB, driver))

			page, err := uc.List(context.Background(), adminbiz.OperationAuditFilter{ActorUserID: 1, Operation: "billing.pricing"}, 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 2, page.Total)
			require.Len(t, page.Items, 1)
			require.Equal(t, "a2", page.Items[0].EventID, "newest matching audit must be first")

			page, err = uc.List(context.Background(), adminbiz.OperationAuditFilter{ActorUserID: 1, Operation: "billing.pricing"}, 2, 1)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, "a1", page.Items[0].EventID)

			page, err = uc.List(context.Background(), adminbiz.OperationAuditFilter{ResourceID: "100", Result: "success"}, 1, 20)
			require.NoError(t, err)
			require.EqualValues(t, 2, page.Total)
			require.Equal(t, "a3", page.Items[0].EventID)

			page, err = uc.List(context.Background(), adminbiz.OperationAuditFilter{StartTimeMS: 1500, EndTimeMS: 3500}, 1, 20)
			require.NoError(t, err)
			require.EqualValues(t, 2, page.Total)
			require.Equal(t, []string{"a3", "a2"}, []string{page.Items[0].EventID, page.Items[1].EventID})
		})
	}
}
