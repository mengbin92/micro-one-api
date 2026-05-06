package config

import appregistry "micro-one-api/internal/pkg/registry"

// Config holds the relay-gateway configuration.
type Config struct {
	Server   ServerConfig         `json:"server"`
	Clients  ClientsConfig        `json:"clients"`
	Retry    RetryConfig          `json:"retry"`
	Models   ModelsConfig         `json:"models"`
	Registry appregistry.Config   `json:"registry"`
}

// ModelsConfig holds model mapping configuration.
type ModelsConfig struct {
	Path string `json:"path"`
}

type ServerConfig struct {
	HTTP HTTPConfig `json:"http"`
	GRPC GRPCConfig `json:"grpc"`
}

type HTTPConfig struct {
	Addr string `json:"addr"`
}

type GRPCConfig struct {
	Addr string `json:"addr"`
}

type ClientsConfig struct {
	Identity identityClientConfig `json:"identity"`
	Channel  channelClientConfig  `json:"channel"`
	Billing  billingClientConfig  `json:"billing"`
}

type identityClientConfig struct {
	Endpoint string `json:"endpoint"`
}

type channelClientConfig struct {
	Endpoint string `json:"endpoint"`
}

type billingClientConfig struct {
	Endpoint string `json:"endpoint"`
}

// RetryConfig holds retry configuration for upstream provider calls.
type RetryConfig struct {
	MaxAttempts     int     `json:"max_attempts"`
	InitialInterval string  `json:"initial_interval"`
	MaxInterval     string  `json:"max_interval"`
	Multiplier      float64 `json:"multiplier"`
	RetryableStatus []int   `json:"retryable_status"`
}

// GetMaxAttempts returns max retry attempts with default.
func (r RetryConfig) GetMaxAttempts() int {
	if r.MaxAttempts <= 0 {
		return 3
	}
	return r.MaxAttempts
}

// GetInitialInterval returns initial interval with default.
func (r RetryConfig) GetInitialInterval() string {
	if r.InitialInterval == "" {
		return "500ms"
	}
	return r.InitialInterval
}

// GetMaxInterval returns max interval with default.
func (r RetryConfig) GetMaxInterval() string {
	if r.MaxInterval == "" {
		return "5s"
	}
	return r.MaxInterval
}

// GetMultiplier returns multiplier with default.
func (r RetryConfig) GetMultiplier() float64 {
	if r.Multiplier <= 0 {
		return 2.0
	}
	return r.Multiplier
}

// GetRetryableStatus returns retryable HTTP status codes with default.
func (r RetryConfig) GetRetryableStatus() []int {
	if len(r.RetryableStatus) == 0 {
		return []int{429, 500, 502, 503}
	}
	return r.RetryableStatus
}
