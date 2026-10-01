package service

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/monitor/internal/biz"
	"micro-one-api/domain/authorization"
)

func TestMonitorDomainHTTPStatus(t *testing.T) {
	w := httptest.NewRecorder()
	writeMonitorError(w, biz.ErrAlertRuleNotFound)
	require.Equal(t, 404, w.Code)
	w = httptest.NewRecorder()
	writeMonitorError(w, biz.ErrInvalidAlertRule)
	require.Equal(t, 400, w.Code)
	w = httptest.NewRecorder()
	writeMonitorError(w, authorization.ErrDenied)
	require.Equal(t, 403, w.Code)
}
