package auth

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	applogger "micro-one-api/internal/pkg/logger"
	"go.uber.org/zap"
)

// JWTClaims represents JWT claims
type JWTClaims struct {
	ServiceName string   `json:"service_name"`
	ServiceType string   `json:"service_type"`
	Roles       []string `json:"roles"`
	jwt.RegisteredClaims
}

// JWTManager manages JWT token creation and validation
type JWTManager struct {
	secretKey      []byte
	issuer         string
	tokenDuration  time.Duration
	refreshDuration time.Duration
}

// NewJWTManager creates a new JWT manager
func NewJWTManager() (*JWTManager, error) {
	secretKey := os.Getenv("JWT_SECRET_KEY")
	if secretKey == "" {
		// Generate a default secret key for development
		secretKey = "dev-secret-key-change-in-production"
		applogger.Log.Warn("Using default JWT secret key - change for production!")
	}

	issuer := os.Getenv("JWT_ISSUER")
	if issuer == "" {
		issuer = "micro-one-api"
	}

	tokenDuration := 24 * time.Hour
	if durationStr := os.Getenv("JWT_TOKEN_DURATION"); durationStr != "" {
		if duration, err := time.ParseDuration(durationStr); err == nil {
			tokenDuration = duration
		}
	}

	return &JWTManager{
		secretKey:      []byte(secretKey),
		issuer:         issuer,
		tokenDuration:  tokenDuration,
		refreshDuration: 7 * 24 * time.Hour, // 7 days
	}, nil
}

// GenerateServiceToken generates a JWT token for a service
func (jm *JWTManager) GenerateServiceToken(serviceName, serviceType string, roles []string) (string, error) {
	now := time.Now()
	claims := JWTClaims{
		ServiceName: serviceName,
		ServiceType: serviceType,
		Roles:       roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jm.issuer,
			Subject:   serviceName,
			Audience:  []string{"micro-one-api"},
			ExpiresAt: jwt.NewNumericDate(now.Add(jm.tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(jm.secretKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	applogger.Log.Info("Generated service token",
		zap.String("service", serviceName),
		zap.String("type", serviceType),
		zap.Strings("roles", roles),
	)

	return signedToken, nil
}

// ValidateServiceToken validates a service JWT token
func (jm *JWTManager) ValidateServiceToken(tokenString string) (*JWTClaims, error) {
	// Remove "Bearer " prefix if present
	tokenString = strings.TrimPrefix(tokenString, "Bearer ")

	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jm.secretKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		// Validate claims
		if claims.Issuer != jm.issuer {
			return nil, fmt.Errorf("invalid issuer: %s", claims.Issuer)
		}

		// Check audience
		if !contains(claims.Audience, "micro-one-api") {
			return nil, fmt.Errorf("invalid audience")
		}

		// Check expiration
		if time.Now().After(claims.ExpiresAt.Time) {
			return nil, fmt.Errorf("token expired")
		}

		return claims, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// RefreshToken refreshes a JWT token
func (jm *JWTManager) RefreshToken(tokenString string) (string, error) {
	claims, err := jm.ValidateServiceToken(tokenString)
	if err != nil {
		return "", fmt.Errorf("invalid token for refresh: %w", err)
	}

	// Generate new token with same claims but new expiration
	return jm.GenerateServiceToken(claims.ServiceName, claims.ServiceType, claims.Roles)
}

// ExtractTokenFromHeader extracts JWT token from authorization header
func ExtractTokenFromHeader(authHeader string) string {
	if authHeader == "" {
		return ""
	}

	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	return authHeader
}

// ServiceAuthConfig holds service authentication configuration
type ServiceAuthConfig struct {
	ServiceName string
	ServiceType string
	Roles       []string
	Token       string
}

// LoadServiceAuthConfig loads service authentication from environment
func LoadServiceAuthConfig() (*ServiceAuthConfig, error) {
	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		serviceName = "unknown-service"
	}

	serviceType := os.Getenv("SERVICE_TYPE")
	if serviceType == "" {
		serviceType = "api"
	}

	roles := strings.Split(os.Getenv("SERVICE_ROLES"), ",")
	for i, role := range roles {
		roles[i] = strings.TrimSpace(role)
	}

	// Get or generate token
	token := os.Getenv("SERVICE_TOKEN")
	if token == "" {
		jm, err := NewJWTManager()
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT manager: %w", err)
		}

		token, err = jm.GenerateServiceToken(serviceName, serviceType, roles)
		if err != nil {
			return nil, fmt.Errorf("failed to generate service token: %w", err)
		}

		applogger.Log.Warn("Generated service token - set SERVICE_TOKEN in production",
			zap.String("token", token[:10]+"..."),
		)
	}

	return &ServiceAuthConfig{
		ServiceName: serviceName,
		ServiceType: serviceType,
		Roles:       roles,
		Token:       token,
	}, nil
}

// HasRole checks if claims contain a specific role
func (c *JWTClaims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole checks if claims contain any of the specified roles
func (c *JWTClaims) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// HasAllRoles checks if claims contain all of the specified roles
func (c *JWTClaims) HasAllRoles(roles ...string) bool {
	for _, role := range roles {
		if !c.HasRole(role) {
			return false
		}
	}
	return true
}

// IsAdmin checks if the service has admin role
func (c *JWTClaims) IsAdmin() bool {
	return c.HasRole("admin")
}

// CanAccess checks if the service can access a resource based on roles
func (c *JWTClaims) CanAccess(resource string, action string) bool {
	// Simple RBAC - can be extended
	switch resource {
	case "admin":
		return c.IsAdmin()
	case "api":
		return c.HasRole("api") || c.IsAdmin()
	case "service":
		return c.HasRole("service") || c.IsAdmin()
	default:
		return c.HasRole("service") || c.IsAdmin()
	}
}

// contains checks if a string slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ValidateServiceTokenWithRoles validates a token and checks if it has required roles
func (jm *JWTManager) ValidateServiceTokenWithRoles(tokenString string, requiredRoles []string) (*JWTClaims, error) {
	claims, err := jm.ValidateServiceToken(tokenString)
	if err != nil {
		return nil, err
	}

	// Check required roles
	for _, requiredRole := range requiredRoles {
		if !claims.HasRole(requiredRole) {
			return nil, fmt.Errorf("missing required role: %s", requiredRole)
		}
	}

	return claims, nil
}
