package authz_test

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"net/http"
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

func TestOwnerReasonTransportRejectsAmbiguity(t *testing.T) {
	t.Setenv("SERVICE_CALLER_TOKENS", "")
	t.Setenv("SERVICE_TOKEN", "")
	handler := authz.HTTPContext("/api.log.v1.LogService/ExportLogs", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "reviewed retention", authorization.WriteReason(r.Context()))
		w.WriteHeader(204)
	})
	r := httptest.NewRequest("GET", "/v1/logs/export", nil)
	r.Header.Set("Authorization", "Bearer user-session")
	r.Header.Set("x-authorization-reason", "reviewed retention")
	w := httptest.NewRecorder()
	handler(w, r)
	require.Equal(t, 204, w.Code)
	r.Header.Add("x-authorization-reason", "conflicting reason")
	w = httptest.NewRecorder()
	handler(w, r)
	require.Equal(t, 401, w.Code)
	interceptor := authz.OperatorUnaryInterceptor()
	next := func(ctx context.Context, _ any) (any, error) {
		require.Equal(t, "reviewed retention", authorization.WriteReason(ctx))
		return nil, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer user-session", "x-authorization-reason", "reviewed retention"))
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, next)
	require.NoError(t, err)
	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer user-session", "x-authorization-reason", "reviewed retention", "x-authorization-reason", "conflicting reason"))
	_, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, next)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
