package grpc

import (
	"fmt"
	"github.com/sony/gobreaker"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// ServiceBreakerConfig permits finite, per-dependency overrides. It does not
// enable resilience: the relay's existing enabled gate remains authoritative.
func ServiceBreakerConfig(service string, timeout time.Duration) (*BreakerConfig, time.Duration, error) {
	switch service {
	case "identity", "channel", "billing", "log":
	default:
		return nil, 0, fmt.Errorf("unsupported resilience service %q", service)
	}
	prefix := "GRPC_" + strings.ToUpper(service) + "_"
	cfg := DefaultBreakerConfig(service)
	cfg.FallbackStrategy = FallbackReject
	samples := 5
	ratio := 0.6
	if raw := os.Getenv(prefix + "BREAKER_MIN_REQUESTS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			return nil, 0, fmt.Errorf("%sBREAKER_MIN_REQUESTS must be 1..1000", prefix)
		}
		samples = value
	}
	if raw := os.Getenv(prefix + "BREAKER_FAILURE_RATIO"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || value <= 0 || value > 1 {
			return nil, 0, fmt.Errorf("%sBREAKER_FAILURE_RATIO must be in (0,1]", prefix)
		}
		ratio = value
	}
	for _, item := range []struct {
		name string
		dst  *time.Duration
		max  time.Duration
	}{{"BREAKER_COOLDOWN", &cfg.Timeout, time.Hour}, {"TIMEOUT", &timeout, 5 * time.Minute}} {
		if raw := os.Getenv(prefix + item.name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 || value > item.max {
				return nil, 0, fmt.Errorf("%s%s must be positive and <= %s", prefix, item.name, item.max)
			}
			*item.dst = value
		}
	}
	cfg.ReadyToTrip = func(counts gobreaker.Counts) bool {
		return counts.Requests >= uint32(samples) && float64(counts.TotalFailures)/float64(counts.Requests) >= ratio
	}
	return cfg, timeout, nil
}
func ValidateServiceBreakerEnvironment() error {
	for _, service := range []string{"identity", "channel", "billing", "log"} {
		if _, _, err := ServiceBreakerConfig(service, 3*time.Second); err != nil {
			return err
		}
	}
	return nil
}
