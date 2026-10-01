package authz_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
)

func TestOwnerHTTPErrorStatuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		code codes.Code
		want int
	}{
		{"unauthenticated", codes.Unauthenticated, 401},
		{"denied", codes.PermissionDenied, 403},
		{"not found", codes.NotFound, 404},
		{"invalid", codes.InvalidArgument, 400},
		{"conflict", codes.Aborted, 409},
		{"unavailable", codes.Unavailable, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			authz.WriteHTTPError(w, status.Error(tc.code, "private detail"))
			require.Equal(t, tc.want, w.Code)
			require.NotContains(t, w.Body.String(), "private detail")
		})
	}
	w := httptest.NewRecorder()
	authz.WriteHTTPError(w, authorization.ErrDenied)
	require.Equal(t, 403, w.Code)
}
