package timeout

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	// DefaultHTTPTimeout is the default timeout for HTTP requests
	DefaultHTTPTimeout = 30 * time.Second

	// DefaultGRPCTimeout is the default timeout for gRPC calls
	DefaultGRPCTimeout = 10 * time.Second

	// DefaultDBQueryTimeout is the default timeout for database queries
	DefaultDBQueryTimeout = 5 * time.Second

	// DefaultUpstreamTimeout is the default timeout for upstream API calls
	DefaultUpstreamTimeout = 60 * time.Second

	// MaxHTTPTimeout is the maximum allowed HTTP timeout
	MaxHTTPTimeout = 5 * time.Minute

	// MaxGRPCTimeout is the maximum allowed gRPC timeout
	MaxGRPCTimeout = 1 * time.Minute
)

// GetTimeout returns a timeout value from environment variable or default
func GetTimeout(envVar string, defaultValue time.Duration, maxValue time.Duration) time.Duration {
	if timeoutStr := os.Getenv(envVar); timeoutStr != "" {
		if duration, err := time.ParseDuration(timeoutStr); err == nil {
			if maxValue > 0 && duration > maxValue {
				return maxValue
			}
			return duration
		}
	}
	return defaultValue
}

// GetHTTPTimeout returns the HTTP timeout
func GetHTTPTimeout() time.Duration {
	return GetTimeout("HTTP_TIMEOUT", DefaultHTTPTimeout, MaxHTTPTimeout)
}

// GetGRPCTimeout returns the gRPC timeout
func GetGRPCTimeout() time.Duration {
	return GetTimeout("GRPC_TIMEOUT", DefaultGRPCTimeout, MaxGRPCTimeout)
}

// GetDBQueryTimeout returns the database query timeout
func GetDBQueryTimeout() time.Duration {
	return GetTimeout("DB_QUERY_TIMEOUT", DefaultDBQueryTimeout, 0)
}

// GetUpstreamTimeout returns the upstream API timeout
func GetUpstreamTimeout() time.Duration {
	return GetTimeout("UPSTREAM_TIMEOUT", DefaultUpstreamTimeout, 0)
}

// WithTimeout creates a context with a timeout
func WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	// If parent already has a deadline, use the remaining time
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(parent, timeout)
}

// WithHTTPTimeout creates a context with HTTP timeout
func WithHTTPTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(parent, GetHTTPTimeout())
}

// WithGRPCTimeout creates a context with gRPC timeout
func WithGRPCTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(parent, GetGRPCTimeout())
}

// WithDBQueryTimeout creates a context with database query timeout
func WithDBQueryTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(parent, GetDBQueryTimeout())
}

// WithUpstreamTimeout creates a context with upstream API timeout
func WithUpstreamTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(parent, GetUpstreamTimeout())
}

// ParseTimeout parses a timeout string with a default value
func ParseTimeout(timeoutStr string, defaultValue time.Duration) time.Duration {
	if timeoutStr == "" {
		return defaultValue
	}

	duration, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return defaultValue
	}

	return duration
}

// ParseIntTimeout parses a timeout in seconds as an integer
func ParseIntTimeout(secondsStr string, defaultValue time.Duration) time.Duration {
	if secondsStr == "" {
		return defaultValue
	}

	seconds, err := strconv.Atoi(secondsStr)
	if err != nil || seconds <= 0 {
		return defaultValue
	}

	return time.Duration(seconds) * time.Second
}

// ValidateTimeout validates that a timeout is within acceptable bounds
func ValidateTimeout(timeout time.Duration, min, max time.Duration) error {
	if min > 0 && timeout < min {
		return fmt.Errorf("timeout %v is less than minimum %v", timeout, min)
	}
	if max > 0 && timeout > max {
		return fmt.Errorf("timeout %v exceeds maximum %v", timeout, max)
	}
	return nil
}
