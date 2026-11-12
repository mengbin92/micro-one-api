package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/jsonx"
)

type operationAuditHandlerRepo struct {
	filter adminbiz.OperationAuditFilter
}

func (r *operationAuditHandlerRepo) List(_ context.Context, filter adminbiz.OperationAuditFilter) ([]adminbiz.OperationAudit, int64, error) {
	r.filter = filter
	return []adminbiz.OperationAudit{{
		EventID: "event-1", ActorUserID: 7, ActorUsername: "admin", Operation: "billing.pricing.update",
		ResourceID: "1001", DecisionVersions: `{"policy":"3"}`, Reason: "reviewed",
		OccurredAt: 1700000000000, Result: "success",
	}}, 1, nil
}

func TestHandleOperationAuditsReturnsFilteredPage(t *testing.T) {
	repo := &operationAuditHandlerRepo{}
	svc := service.NewAdminService(nil, nil, nil, nil)
	svc.SetOperationAuditUsecase(adminbiz.NewOperationAuditUsecase(repo))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/operation-audits?actor_user_id=7&operation=billing.pricing&result=success&start_time_ms=100&end_time_ms=200&page=2&page_size=50", nil)
	rec := httptest.NewRecorder()

	handleOperationAudits(rec, req, svc)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, adminbiz.OperationAuditFilter{
		ActorUserID: 7, Operation: "billing.pricing", Result: "success", StartTimeMS: 100, EndTimeMS: 200, Offset: 50, Limit: 50,
	}, repo.filter)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Items    []operationAuditResponse `json:"items"`
			Total    int64                    `json:"total"`
			Page     int                      `json:"page"`
			PageSize int                      `json:"page_size"`
		} `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.EqualValues(t, 1, body.Data.Total)
	require.Equal(t, 2, body.Data.Page)
	require.Equal(t, 50, body.Data.PageSize)
	require.Equal(t, "event-1", body.Data.Items[0].EventID)
	require.Equal(t, int64(7), body.Data.Items[0].ActorUserID)
	require.Equal(t, "admin", body.Data.Items[0].ActorUsername)
}

func TestHandleOperationAuditsRejectsInvalidQuery(t *testing.T) {
	svc := service.NewAdminService(nil, nil, nil, nil)
	svc.SetOperationAuditUsecase(adminbiz.NewOperationAuditUsecase(&operationAuditHandlerRepo{}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/operation-audits?result=unknown", nil)
	rec := httptest.NewRecorder()

	handleOperationAudits(rec, req, svc)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/operation-audits?page=0", nil)
	rec = httptest.NewRecorder()
	handleOperationAudits(rec, req, svc)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleOperationAuditsReportsMissingReaderAsUnavailable(t *testing.T) {
	// An admin DB without the audit table (or without the optional DB config)
	// leaves the usecase unset. This is a retryable availability failure, not
	// an internal error, and must not be reported as HTTP 500.
	svc := service.NewAdminService(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/operation-audits", nil)
	rec := httptest.NewRecorder()

	handleOperationAudits(rec, req, svc)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestOperationAuditRouteIsReadyOnlyForGet(t *testing.T) {
	// The console guard denies paths absent from the owner-route allowlist.
	// Guard this read-only endpoint against a future method or prefix regression.
	const path = "/api/v1/admin/operation-audits"
	require.True(t, iamUserRouteReady(httptest.NewRequest(http.MethodGet, path, nil)))
	require.True(t, iamOwnerRouteReady(path, http.MethodGet))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		require.False(t, iamUserRouteReady(httptest.NewRequest(method, path, nil)), method)
		require.False(t, iamOwnerRouteReady(path, method), method)
	}
	require.False(t, iamOwnerRouteReady(path+"/export", http.MethodGet), "no prefix grant for unregistered subpaths")
}
