package authz

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/security/serviceidentity"
	"micro-one-api/platform/security/sessionguard"
)

// OperatorUnaryInterceptor forwards one operator credential into the pure
// domain context. The service-identity interceptor runs before this adapter.
func OperatorUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("x-operator-authorization")
		reasons := slices.Concat(md.Get("x-authorization-reason-bin"), md.Get("x-authorization-reason"))
		if len(values) > 1 || len(reasons) > 1 {
			return nil, status.Error(codes.Unauthenticated, "ambiguous operator credential")
		}
		raw := ""
		if len(values) == 1 {
			raw = values[0]
		}
		reason := ""
		if len(reasons) == 1 {
			reason = reasons[0]
		}
		reply, err := next(authorization.WithWriteReason(authorization.WithCredential(authorization.WithExternal(ctx), raw), reason), req)
		if errors.Is(err, authorization.ErrWriteStorageUnavailable) {
			err = status.Error(codes.Unavailable, "durable resource write storage unavailable")
		}
		if errors.Is(err, authorization.ErrDenied) {
			err = status.Error(codes.PermissionDenied, "authorization denied")
		}
		return reply, err
	}
}

// HTTPContext adapts a fixed owner HTTP method to the same capability checks
// as RPC. Ordinary HTTP users supply their JWT in Authorization. A verified
// service may instead forward a user operator; headers never select a caller.
func HTTPContext(method string, next http.HandlerFunc) http.HandlerFunc {
	verifier := serviceidentity.FromEnvironment(os.Getenv("SERVICE_TOKEN"))
	sessions := sessionguard.FromEnvironment()
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := authorization.WithExternal(r.Context())
		if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("x-operator-authorization")) > 1 || len(r.Header.Values("x-authorization-reason")) > 1 {
			WriteHTTPError(w, status.Error(codes.Unauthenticated, "ambiguous credential"))
			return
		}
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")) == "" {
			WriteHTTPError(w, status.Error(codes.Unauthenticated, "credential required"))
			return
		}
		p, err := verifier.Authenticate(strings.TrimPrefix(raw, "Bearer "))
		ctx = authorization.WithLegacyAuthorization(ctx, err == nil || sessions.LegacyAdmin(raw))
		if err == nil && p.Dedicated {
			if !p.CanCall(method) && !p.CanCallHTTP(method) {
				WriteHTTPError(w, status.Error(codes.PermissionDenied, "caller capability denied"))
				return
			}
			ctx = serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(ctx, p), method)
			raw = r.Header.Get("x-operator-authorization")
		}
		next(w, r.WithContext(authorization.WithWriteReason(authorization.WithCredential(ctx, raw), r.Header.Get("x-authorization-reason"))))
	}
}

func WriteHTTPError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, authorization.ErrWriteConflict) {
		code = http.StatusConflict
	} else if errors.Is(err, authorization.ErrWritePrecondition) {
		code = http.StatusBadRequest
	} else if errors.Is(err, authorization.ErrWriteStorageUnavailable) {
		code = http.StatusServiceUnavailable
	} else if errors.Is(err, authorization.ErrDenied) {
		code = http.StatusForbidden
	} else {
		switch status.Code(err) {
		case codes.Unauthenticated:
			code = 401
		case codes.PermissionDenied:
			code = 403
		case codes.InvalidArgument:
			code = 400
		case codes.NotFound:
			code = 404
		case codes.Aborted, codes.AlreadyExists:
			code = 409
		case codes.Unavailable:
			code = 503
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = jsonx.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(code)})
}

func IsAuthorizationError(err error) bool {
	if errors.Is(err, authorization.ErrWriteConflict) || errors.Is(err, authorization.ErrWritePrecondition) {
		return true
	}
	if errors.Is(err, authorization.ErrWriteStorageUnavailable) {
		return true
	}
	if errors.Is(err, authorization.ErrDenied) {
		return true
	}
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.Aborted, codes.Unavailable:
		return true
	}
	return false
}

// CoverageUnaryInterceptor denies unfinished owner handlers in IAM. The
// fixed method allowlist proves caller capability, while ready lists prove
// an actual resource owner handler has an authorization implementation.
func CoverageUnaryInterceptor(c *Client, point string, ready []string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		p := serviceidentity.FromContext(ctx)
		if serviceidentity.HasSystemCapability(ctx, info.FullMethod) {
			return next(ctx, req)
		}
		mode, err := c.Mode(ctx, point)
		if err != nil {
			return nil, err
		}
		if mode == "iam" && (!p.Dedicated || !p.CanCall(info.FullMethod) || !slices.Contains(ready, info.FullMethod)) {
			return nil, status.Error(codes.PermissionDenied, "owner execution point incomplete or caller denied")
		}
		return next(ctx, req)
	}
}
