package grpc

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
	appauth "micro-one-api/platform/security/auth"
	apptls "micro-one-api/platform/tls"
)

// AuthServer wraps a gRPC server with authentication
type AuthServer struct {
	grpcServer   *grpc.Server
	jwtManager   *appauth.JWTManager
	requireAuth  bool
	requireRoles []string
}

// NewAuthServer creates a new authenticated gRPC server
func NewAuthServer(
	server *grpc.Server,
	jwtManager *appauth.JWTManager,
	requireAuth bool,
	requireRoles []string,
) *AuthServer {
	return &AuthServer{
		grpcServer:   server,
		jwtManager:   jwtManager,
		requireAuth:  requireAuth,
		requireRoles: requireRoles,
	}
}

// GetServer returns the underlying gRPC server
func (a *AuthServer) GetServer() *grpc.Server {
	return a.grpcServer
}

// CreateAuthenticatedServer creates a gRPC server with authentication middleware
func CreateAuthenticatedServer(
	options []grpc.ServerOption,
	tlsConfig *apptls.TLSConfig,
	requireAuth bool,
) (*grpc.Server, *appauth.JWTManager, error) {
	// Create JWT manager
	jwtManager, err := appauth.NewJWTManager()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create JWT manager: %w", err)
	}

	// Add TLS credentials if enabled
	if tlsConfig.Enabled {
		creds, err := apptls.CreateServerCredentials(tlsConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create server credentials: %w", err)
		}
		options = append(options, grpc.Creds(creds))
	}

	// Add authentication interceptor if required
	if requireAuth {
		authInterceptor := NewAuthInterceptor(jwtManager)
		options = append(options, grpc.ChainUnaryInterceptor(authInterceptor.Unary),
			grpc.ChainStreamInterceptor(authInterceptor.Stream))
	}

	// Create server
	server := grpc.NewServer(options...)

	return server, jwtManager, nil
}

// AuthInterceptor handles authentication for gRPC calls
type AuthInterceptor struct {
	jwtManager   *appauth.JWTManager
	requireRoles []string
}

// NewAuthInterceptor creates a new authentication interceptor
func NewAuthInterceptor(jwtManager *appauth.JWTManager) *AuthInterceptor {
	return &AuthInterceptor{
		jwtManager:   jwtManager,
		requireRoles: []string{},
	}
}

// WithRoles returns a new interceptor that requires specific roles
func (a *AuthInterceptor) WithRoles(roles ...string) *AuthInterceptor {
	return &AuthInterceptor{
		jwtManager:   a.jwtManager,
		requireRoles: roles,
	}
}

// Unary intercepts unary RPC calls
func (a *AuthInterceptor) Unary(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	// Validate authentication
	if err := a.validateRequest(ctx, info.FullMethod); err != nil {
		applogger.Log.Warn("Authentication failed",
			zap.String("method", info.FullMethod),
			zap.Error(err),
			zap.String("request_id", getRequestIDFromContext(ctx)),
		)
		return nil, err
	}

	return handler(ctx, req)
}

// Stream intercepts streaming RPC calls
func (a *AuthInterceptor) Stream(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	// Validate authentication
	if err := a.validateRequest(ss.Context(), info.FullMethod); err != nil {
		applogger.Log.Warn("Authentication failed",
			zap.String("method", info.FullMethod),
			zap.Error(err),
			zap.String("request_id", getRequestIDFromContext(ss.Context())),
		)
		return err
	}

	return handler(srv, ss)
}

// validateRequest validates the authentication request
func (a *AuthInterceptor) validateRequest(ctx context.Context, method string) error {
	// Extract token (and mTLS flag) from metadata / peer certificates.
	token, mtlsAuthenticated, err := extractTokenFromContext(ctx)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "missing authentication token: %v", err)
	}

	var claims *appauth.JWTClaims
	if mtlsAuthenticated {
		// L3: a verified mTLS peer certificate already authenticated the
		// caller at the transport layer. Skip JWT validation so the mTLS
		// path is not a dead branch that always fails (the previous code
		// logged "mTLS authentication successful" but then ran
		// ValidateServiceToken("") which always rejected the caller).
		claims = &appauth.JWTClaims{
			ServiceName: "mtls-peer",
			ServiceType: "mtls",
		}
	} else {
		c, err := a.jwtManager.ValidateServiceToken(token)
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "invalid authentication token: %v", err)
		}
		claims = c
	}

	// Check required roles
	if len(a.requireRoles) > 0 {
		for _, role := range a.requireRoles {
			if !claims.HasRole(role) {
				applogger.Log.Warn("Insufficient permissions",
					zap.String("service", claims.ServiceName),
					zap.Strings("required_roles", a.requireRoles),
					zap.Strings("user_roles", claims.Roles),
					zap.String("method", method),
				)
				return status.Errorf(codes.PermissionDenied, "insufficient permissions: missing role %s", role)
			}
		}
	}

	// Log successful authentication
	applogger.Log.Debug("Authentication successful",
		zap.String("service", claims.ServiceName),
		zap.String("type", claims.ServiceType),
		zap.Strings("roles", claims.Roles),
		zap.String("method", method),
	)

	return nil
}

// extractTokenFromContext extracts the JWT token from gRPC metadata and
// reports whether the caller authenticated via a verified mTLS peer
// certificate. When mtlsAuthenticated is true the returned token is empty and
// the caller should skip JWT validation (the transport already authenticated
// the peer) — review L3.
func extractTokenFromContext(ctx context.Context) (string, bool, error) {
	// Check for mTLS client certificates
	p, ok := peer.FromContext(ctx)
	if ok && p.AuthInfo != nil {
		if tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			if len(tlsInfo.State.PeerCertificates) > 0 {
				applogger.Log.Debug("mTLS authentication successful")
				return "", true, nil // No JWT needed for mTLS
			}
		}
	}

	// Read JWT from gRPC incoming metadata "authorization" key
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false, fmt.Errorf("no gRPC metadata in context")
	}

	values := md.Get("authorization")
	if len(values) == 0 {
		return "", false, fmt.Errorf("authorization metadata not found")
	}

	authHeader := values[0]
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", false, fmt.Errorf("invalid authorization format, expected Bearer token")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return "", false, fmt.Errorf("empty Bearer token")
	}

	return token, false, nil
}

// getRequestIDFromContext extracts request ID from context
func getRequestIDFromContext(ctx context.Context) string {
	// This would extract request ID from context if we add it
	// For now, return a placeholder
	return "unknown"
}

// CreateAuthenticatedClient creates a gRPC client with authentication
func CreateAuthenticatedClient(
	target string,
	tlsConfig *apptls.TLSConfig,
	serviceAuth *appauth.ServiceAuthConfig,
) (*grpc.ClientConn, error) {
	// Create TLS credentials
	creds, err := apptls.CreateClientCredentials(tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create client credentials: %w", err)
	}

	// Create dial options
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}

	// Add JWT token as per-RPC credentials if provided
	if serviceAuth != nil && serviceAuth.Token != "" {
		tokenCreds := NewTokenAuth(serviceAuth.Token)
		opts = append(opts, grpc.WithPerRPCCredentials(tokenCreds))
	}

	// Connect to server
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return conn, nil
}

// TokenAuth implements PerRPCCredentials for JWT token authentication
type TokenAuth struct {
	token string
}

// NewTokenAuth creates a new token auth credentials
func NewTokenAuth(token string) *TokenAuth {
	return &TokenAuth{token: token}
}

// GetRequestMetadata adds the token to the request metadata
func (t *TokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

// RequireTransportSecurity indicates whether the credentials require transport security
func (t *TokenAuth) RequireTransportSecurity() bool {
	return true // Always require TLS for JWT tokens
}
