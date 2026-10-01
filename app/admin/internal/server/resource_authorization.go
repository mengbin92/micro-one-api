package server

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"strconv"
	"strings"
)

type iamBusinessContextKey struct{}

func iamBusinessContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, iamBusinessContextKey{}, true)
}
func isIAMBusinessContext(ctx context.Context) bool {
	value, _ := ctx.Value(iamBusinessContextKey{}).(bool)
	return value
}

// Only completed business slices can pass the console IAM guard. Each handler
// still obtains its precise resource/field decision from the owning service.
// Keep unknown routes and unfinished B2/B3/B4 handlers fail closed in IAM.
func iamUserRouteReady(r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(path, "/api/v1/admin/routing-access/") {
		id, err := strconv.ParseInt(strings.TrimPrefix(path, "/api/v1/admin/routing-access/"), 10, 64)
		return err == nil && id > 0 && (r.Method == http.MethodGet || r.Method == http.MethodPatch)
	}
	if path == "/api/admin/access" {
		return r.Method == http.MethodGet
	}
	for _, base := range []string{"/v1/users", "/api/user"} {
		if path == base || path == base+"/search" {
			return r.Method == http.MethodGet || (path == base && (r.Method == http.MethodPut || r.Method == http.MethodPost))
		}
		if strings.HasPrefix(path, base+"/") {
			id, err := strconv.ParseInt(strings.TrimPrefix(path, base+"/"), 10, 64)
			if err == nil && id > 0 {
				return r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete
			}
		}
	}
	return false
}

func resourceHTTPErrorCode(err error) (int, bool) {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return http.StatusUnauthorized, true
	case codes.PermissionDenied:
		return http.StatusForbidden, true
	case codes.Aborted, codes.AlreadyExists:
		return http.StatusConflict, true
	case codes.InvalidArgument:
		return http.StatusBadRequest, true
	case codes.Unavailable:
		return http.StatusServiceUnavailable, true
	default:
		return 0, false
	}
}
