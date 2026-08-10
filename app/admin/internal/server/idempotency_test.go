package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"micro-one-api/app/admin/internal/service"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestWriteSubscriptionResponse_AlreadyExistsIs409 guards v0.18 P0 §5.4 on the
// HTTP boundary: a downstream gRPC AlreadyExists (duplicate idempotency key)
// must surface as HTTP 409 Conflict with the business message, so the frontend
// retry logic (which clears its key on 409) actually receives it.
func TestWriteSubscriptionResponse_AlreadyExistsIs409(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSubscriptionResponse(rec, nil, status.Error(codes.AlreadyExists, "duplicate request"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "duplicate request")
	require.Contains(t, rec.Body.String(), "false")
}

// TestWriteSubscriptionResponse_BusinessErrorKeeps200 verifies local business
// errors (e.g. "not purchasable", insufficient balance) keep the legacy
// 200 + success:false shape with the raw message.
func TestWriteSubscriptionResponse_BusinessErrorKeeps200(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSubscriptionResponse(rec, nil, errors.New("subscription group is not available for purchase"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "subscription group is not available")
}

// TestWriteServiceResponse_AlreadyExistsIs409 guards the admin recharge path
// (handleTopUp -> writeServiceResponse) mapping.
func TestWriteServiceResponse_AlreadyExistsIs409(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceResponse(rec, nil, status.Error(codes.AlreadyExists, "duplicate request"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "duplicate request")
}

// TestHttpStatusForAdminError pins the status-code mapping table.
func TestHttpStatusForAdminError(t *testing.T) {
	require.Equal(t, http.StatusConflict, httpStatusForAdminError(status.Error(codes.AlreadyExists, "dup")))
	require.Equal(t, http.StatusNotFound, httpStatusForAdminError(status.Error(codes.NotFound, "nf")))
	require.Equal(t, http.StatusBadRequest, httpStatusForAdminError(status.Error(codes.InvalidArgument, "bad")))
	require.Equal(t, http.StatusOK, httpStatusForAdminError(errors.New("local business error")))
	require.Equal(t, http.StatusNotImplemented, httpStatusForAdminError(service.ErrSubscriptionServiceNotConfigured))
	require.Equal(t, http.StatusOK, httpStatusForAdminError(nil))
}
