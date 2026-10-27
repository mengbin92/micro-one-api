package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/status"
	"micro-one-api/pkg/jsonx"
)

func TestAdminWriteResponsePreservesSecurityVerificationReason(t *testing.T) {
	for _, reason := range []string{"STEP_UP_REQUIRED", "MFA_ENROLLMENT_REQUIRED", "SCOPE_DENIED"} {
		t.Run(reason, func(t *testing.T) {
			// Recreate the downstream gRPC status, including its ErrorInfo detail.
			err := status.ErrorProto(status.Convert(kerrors.Forbidden(reason, "verification required")).Proto())
			rec := httptest.NewRecorder()
			writeServiceResponse(rec, nil, err)
			require.Equal(t, http.StatusForbidden, rec.Code)
			var payload map[string]any
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &payload))
			require.Equal(t, false, payload["success"])
			if reason == "SCOPE_DENIED" {
				require.NotContains(t, payload, "reason")
			} else {
				require.Equal(t, reason, payload["reason"])
			}
		})
	}
}
