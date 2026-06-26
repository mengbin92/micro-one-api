package config

import appregistry "micro-one-api/internal/pkg/registry"

// Config holds the relay-gateway configuration.
type Config struct {
	Server        ServerConfig       `json:"server"`
	Clients       ClientsConfig      `json:"clients"`
	Retry         RetryConfig        `json:"retry"`
	Models        ModelsConfig       `json:"models" yaml:"models"`
	Registry      appregistry.Config `json:"registry"`
	OpenAIWS      OpenAIWSConfig     `json:"openai_ws" yaml:"openai_ws"`
	HybridAdaptor HybridAdaptorConfig `json:"hybrid_adaptor" yaml:"hybrid_adaptor"`
}

// HybridAdaptorConfig controls the hybrid adaptor layer (plan §十). The
// feature flag Enabled gates whether the new adaptor-based request path is
// used; when false (the default) the gateway keeps using the existing
// provider-factory path unchanged, so the MVP can ship behind the flag and be
// rolled back instantly.
type HybridAdaptorConfig struct {
	// Enabled turns on the hybrid adaptor request path. When false, the
	// relay gateway behaves exactly as before (provider-factory direct call).
	Enabled bool `json:"enabled" yaml:"enabled"`

	// IdentityTTL is the TTL for cached subscription-account fingerprints. A
	// zero value caches indefinitely (the in-process default).
	IdentityTTL string `json:"identity_ttl" yaml:"identity_ttl"`

	// RefreshInterval is how often the background token-refresh task scans
	// for soon-to-expire accounts. Defaults to 10m.
	RefreshInterval string `json:"refresh_interval" yaml:"refresh_interval"`

	// RefreshLookahead is how far ahead the refresh task looks for expiring
	// accounts. Defaults to 24h.
	RefreshLookahead string `json:"refresh_lookahead" yaml:"refresh_lookahead"`
}

// GetHybridAdaptorEnabled reports whether the hybrid adaptor path is enabled.
func (c HybridAdaptorConfig) GetHybridAdaptorEnabled() bool { return c.Enabled }

// GetIdentityTTL returns the fingerprint cache TTL with default.
func (c HybridAdaptorConfig) GetIdentityTTL() string {
	if c.IdentityTTL == "" {
		return "24h"
	}
	return c.IdentityTTL
}

// GetRefreshInterval returns the background refresh interval with default.
func (c HybridAdaptorConfig) GetRefreshInterval() string {
	if c.RefreshInterval == "" {
		return "10m"
	}
	return c.RefreshInterval
}

// GetRefreshLookahead returns the refresh lookahead window with default.
func (c HybridAdaptorConfig) GetRefreshLookahead() string {
	if c.RefreshLookahead == "" {
		return "24h"
	}
	return c.RefreshLookahead
}

// OpenAIWSConfig holds tunables for the Codex Responses WebSocket relay
// (inbound upgrade on /v1/responses). All fields are optional; zero values
// fall back to sensible defaults in the relay server.
type OpenAIWSConfig struct {
	// WriteTimeout is the per-frame write deadline for client<->upstream pumps.
	WriteTimeout string `json:"write_timeout" yaml:"write_timeout"`
	// IdleTimeout is how long the relay waits for activity before closing.
	IdleTimeout string `json:"idle_timeout" yaml:"idle_timeout"`
	// DialTimeout is the upstream WebSocket dial deadline.
	DialTimeout string `json:"dial_timeout" yaml:"dial_timeout"`
	// FirstMessageTimeout is how long to wait for the client's first
	// response.create frame after the upgrade completes.
	FirstMessageTimeout string `json:"first_message_timeout" yaml:"first_message_timeout"`
	// MaxConnsPerChannel caps idle upstream connections per channel in the pool.
	MaxConnsPerChannel int `json:"max_conns_per_channel" yaml:"max_conns_per_channel"`
	// FailoverMaxSwitches is how many alternative channels to try on a
	// retryable upstream error before surfacing the error to the client.
	FailoverMaxSwitches int `json:"failover_max_switches" yaml:"failover_max_switches"`
	// StickyTTL is the TTL for response->channel sticky bindings (local + Redis).
	StickyTTL string `json:"sticky_ttl" yaml:"sticky_ttl"`
	// RedisAddr enables the cross-process sticky store. Empty = in-memory only.
	RedisAddr string `json:"redis_addr" yaml:"redis_addr"`
	// RedisPassword for the sticky store.
	RedisPassword string `json:"redis_password" yaml:"redis_password"`
}

// ModelsConfig holds model mapping configuration.
type ModelsConfig struct {
	Path string `json:"path" yaml:"path"`
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
	Log      logClientConfig      `json:"log"`
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

type logClientConfig struct {
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

// GetOpenAIWSWriteTimeout returns the write timeout with default fallback.
func (c OpenAIWSConfig) GetOpenAIWSWriteTimeout() string {
	if c.WriteTimeout == "" {
		return "2m"
	}
	return c.WriteTimeout
}

// GetOpenAIWSIdleTimeout returns the idle timeout with default fallback.
func (c OpenAIWSConfig) GetOpenAIWSIdleTimeout() string {
	if c.IdleTimeout == "" {
		return "5m"
	}
	return c.IdleTimeout
}

// GetOpenAIWSDialTimeout returns the dial timeout with default fallback.
func (c OpenAIWSConfig) GetOpenAIWSDialTimeout() string {
	if c.DialTimeout == "" {
		return "30s"
	}
	return c.DialTimeout
}

// GetOpenAIWSFirstMessageTimeout returns the first-message timeout with default.
func (c OpenAIWSConfig) GetOpenAIWSFirstMessageTimeout() string {
	if c.FirstMessageTimeout == "" {
		return "30s"
	}
	return c.FirstMessageTimeout
}

// GetOpenAIWSMaxConnsPerChannel returns the per-channel pool cap with default.
func (c OpenAIWSConfig) GetOpenAIWSMaxConnsPerChannel() int {
	if c.MaxConnsPerChannel <= 0 {
		return 8
	}
	return c.MaxConnsPerChannel
}

// GetOpenAIWSFailoverMaxSwitches returns the failover switch limit with default.
func (c OpenAIWSConfig) GetOpenAIWSFailoverMaxSwitches() int {
	if c.FailoverMaxSwitches <= 0 {
		return 2
	}
	return c.FailoverMaxSwitches
}

// GetOpenAIWSStickyTTL returns the sticky binding TTL with default.
func (c OpenAIWSConfig) GetOpenAIWSStickyTTL() string {
	if c.StickyTTL == "" {
		return "1h"
	}
	return c.StickyTTL
}
