package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type slowOptionsRepo struct{}

func (slowOptionsRepo) Get(ctx context.Context, key string) (string, error) {
	if key != "SystemName" {
		return "", nil
	}
	timer := time.NewTimer(1100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "slow but valid config", nil
	}
}
func (slowOptionsRepo) Set(context.Context, string, string) error { return nil }

// The real HTTP filter must let a valid multi-item options read finish after
// one second. Calling the handler directly would miss Kratos's default deadline.
func TestAdminHTTPOptionsSurviveDefaultKratosDeadline(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "timeout-fixture")
	uc := adminbiz.NewSystemOptionsUsecase(slowOptionsRepo{})
	svc := service.NewAdminService(nil, nil, nil, uc)
	srv := httptest.NewServer(NewHTTPServer("", svc, nil))
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/option/", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer timeout-fixture")
	response, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	require.Contains(t, string(body), "slow but valid config")
}
