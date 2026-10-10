package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionQuotaPolicyHTTPWritePreconditions(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := subscriptiondata.NewRepository(db, nil)
	group := &subscriptionbiz.SubscriptionGroup{Name: "policy", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(context.Background(), group))
	svc := service.NewAdminService(nil, nil, nil, nil)
	svc.SetSubscriptionUsecases(nil, subscriptionbiz.NewGroupUsecase(repo))
	t.Run("list returns owner revision", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handleSubscriptionGroups(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/subscription-groups", nil), svc)
		var reply struct {
			Data []map[string]any `json:"data"`
		}
		require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &reply))
		require.Len(t, reply.Data, 1)
		require.EqualValues(t, 1, reply.Data[0]["revision"])
	})
	t.Run("update accepts caller revision and rejects stale retry", func(t *testing.T) {
		ctx := authorization.WithQueryScope(authorization.WithWriteReason(context.Background(), "reviewed"), "subscription.quota_policy.update", authztest.Resources(group.ID))
		update := func() bool {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/subscription-groups/1", strings.NewReader(`{"name":"renamed","status":1,"rate_multiplier":1,"expected_revision":1}`)).WithContext(ctx)
			rec := httptest.NewRecorder()
			handleSubscriptionGroupByID(rec, req, svc)
			var reply struct {
				Success bool `json:"success"`
			}
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &reply))
			return reply.Success
		}
		require.True(t, update(), "valid displayed revision must reach the owner")
		require.False(t, update(), "a stale retry must fail")
	})
	t.Run("plan update accepts caller revision and rejects stale retry", func(t *testing.T) {
		plan := &subscriptionbiz.SubscriptionPlan{Name: "monthly", GroupID: group.ID, ForSale: true, ValidityDays: 30}
		require.NoError(t, repo.CreatePlan(context.Background(), plan))
		svc.SetSubscriptionUsecases(nil, subscriptionbiz.NewGroupUsecase(repo), subscriptionbiz.NewPlanUsecase(repo, repo))
		ctx := authorization.WithQueryScope(authorization.WithWriteReason(context.Background(), "reviewed"), "subscription.plan.update", authztest.Resources(plan.ID))
		update := func() bool {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/subscription-plans/1", strings.NewReader(`{"name":"renamed","group_id":1,"for_sale":true,"validity_days":30,"expected_revision":1}`)).WithContext(ctx)
			rec := httptest.NewRecorder()
			handleSubscriptionPlanByID(rec, req, svc)
			var reply struct {
				Success bool `json:"success"`
			}
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &reply))
			return reply.Success
		}
		require.True(t, update(), "valid displayed revision must reach the owner")
		require.False(t, update(), "a stale retry must fail")
	})
}

func TestSubscriptionGroupCreatePreservesExplicitDisabledStatus(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	for _, tt := range []struct {
		body string
		want int32
	}{
		{`{"name":"quota-policy"}`, 1},
		{`{"name":"quota-policy","status":0}`, 0},
		{`{"name":"quota-policy","status":1}`, 1},
	} {
		t.Run(tt.body, func(t *testing.T) {
			srv := newPlanLifecycleTestServer()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-groups", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer admin-token")
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			srv.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var created struct {
				Success bool                 `json:"success"`
				Data    subscriptionGroupDTO `json:"data"`
			}
			require.NoError(t, jsonx.Unmarshal(recorder.Body.Bytes(), &created))
			require.True(t, created.Success, recorder.Body.String())
			require.Equal(t, tt.want, created.Data.Status)

			req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/subscription-groups/1", nil)
			req.Header.Set("Authorization", "Bearer admin-token")
			recorder = httptest.NewRecorder()
			srv.ServeHTTP(recorder, req)
			require.NoError(t, jsonx.Unmarshal(recorder.Body.Bytes(), &created))
			require.True(t, created.Success)
			require.Equal(t, tt.want, created.Data.Status, "persisted policy status must match the request")
		})
	}
}
