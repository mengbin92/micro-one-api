package config

import (
	"fmt"

	appregistry "micro-one-api/internal/pkg/registry"
)

// Config holds the relay-gateway configuration.
type Config struct {
	Server            ServerConfig            `json:"server"`
	Clients           ClientsConfig           `json:"clients"`
	Retry             RetryConfig             `json:"retry"`
	Models            ModelsConfig            `json:"models" yaml:"models"`
	Registry          appregistry.Config      `json:"registry"`
	Redis             RedisConfig             `json:"redis" yaml:"redis"`
	OpenAIWS          OpenAIWSConfig          `json:"openai_ws" yaml:"openai_ws"`
	HybridAdaptor     HybridAdaptorConfig     `json:"hybrid_adaptor" yaml:"hybrid_adaptor"`
	SessionSticky     SessionStickyConfig     `json:"session_sticky" yaml:"session_sticky"`
	Subscription      SubscriptionConfig      `json:"subscription" yaml:"subscription"`
	RelayOrchestrator RelayOrchestratorConfig `json:"relay_orchestrator" yaml:"relay_orchestrator"`
	ChannelCache      ChannelCacheConfig      `json:"channel_cache" yaml:"channel_cache"`
	Idempotency       IdempotencyConfig       `json:"idempotency" yaml:"idempotency"`
	Audit             AuditConfig             `json:"audit" yaml:"audit"`
	Resilience        ResilienceConfig        `json:"resilience" yaml:"resilience"`
	MTLS              MTLSConfig              `json:"mtls" yaml:"mtls"`
}

// SubscriptionConfig controls user subscription quota enforcement at the
// relay-gateway request entry. Disabled by default.
type SubscriptionConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

func (c SubscriptionConfig) GetSubscriptionEnabled() bool { return c.Enabled }

// SessionStickyConfig gates cross-session subscription-account stickiness
// (bug docs/sub2api-borrowable-ideas.md #7): binding a conversation
// (session_hash) to the subscription account that served it so subsequent
// turns reuse the same upstream account and hit its prompt cache. Disabled by
// default. It is a routing-behavior change kept behind its own switch so it can
// be rolled out / rolled back independently of the hybrid adaptor path; it only
// takes effect when the hybrid adaptor path is enabled (bind happens in the
// adaptor failover loop). The binding TTL reuses OpenAIWSConfig.StickyTTL.
type SessionStickyConfig struct {
	// Enabled turns on session -> subscription-account stickiness for the
	// chat-completions and anthropic-messages entry points.
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// GetSessionStickyEnabled reports whether session-account stickiness is enabled.
func (c SessionStickyConfig) GetSessionStickyEnabled() bool { return c.Enabled }

// RelayOrchestratorConfig controls the handler -> orchestrator -> forwarder
// route for chat completions. Disabled by default.
type RelayOrchestratorConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// GetRelayOrchestratorEnabled reports whether the orchestrator route is enabled.
func (c RelayOrchestratorConfig) GetRelayOrchestratorEnabled() bool { return c.Enabled }

// ChannelCacheConfig controls the multi-level ChannelCache that fronts the
// channel-service SelectChannel RPC. Disabled by default; when enabled (and
// Redis is configured) it caches channel-selection results per group+model
// to cut channel-service gRPC load on hot models. Failover selections
// (ExcludeFirstPriority=true) always bypass the cache.
type ChannelCacheConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// GetChannelCacheEnabled reports whether the channel cache is enabled.
func (c ChannelCacheConfig) GetChannelCacheEnabled() bool { return c.Enabled }

type IdempotencyConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	TTL     string `json:"ttl" yaml:"ttl"`
}

type AuditConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type ResilienceConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Timeout string `json:"timeout" yaml:"timeout"`
}

type RedisConfig struct {
	Addr     string `json:"addr" yaml:"addr"`
	Password string `json:"password" yaml:"password"`
}

type MTLSConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	CertFile string `json:"cert_file" yaml:"cert_file"`
	KeyFile  string `json:"key_file" yaml:"key_file"`
	CAFile   string `json:"ca_file" yaml:"ca_file"`
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

	// TokenRefresh controls the enhanced background token refresh service.
	TokenRefresh TokenRefreshConfig `json:"token_refresh" yaml:"token_refresh"`

	// RuntimeBlock controls how long a subscription account is cooled down at
	// runtime after a retryable upstream failure, per status class.
	RuntimeBlock RuntimeBlockConfig `json:"runtime_block" yaml:"runtime_block"`
}

// RuntimeBlockConfig tunes the relay-gateway runtime blocker (the short-lived
// per-account cool-down applied on retryable upstream failures during
// subscription-account failover). All durations are Go duration strings; empty
// values fall back to the built-in defaults.
type RuntimeBlockConfig struct {
	// RateLimitedDuration cools an account down after a 429. Default 5s.
	RateLimitedDuration string `json:"rate_limited_duration" yaml:"rate_limited_duration"`
	// UnauthorizedDuration cools an account down after a 401. Default 2m.
	UnauthorizedDuration string `json:"unauthorized_duration" yaml:"unauthorized_duration"`
	// ServerErrorDuration cools an account down after a 5xx. Default 2m.
	ServerErrorDuration string `json:"server_error_duration" yaml:"server_error_duration"`
	// OverloadedDuration cools an account down after a 529 (upstream Overloaded).
	// Distinct from a 429/5xx: the account is not over quota and the upstream is
	// only momentarily saturated, so the default is short. Default 30s.
	OverloadedDuration string `json:"overloaded_duration" yaml:"overloaded_duration"`
	// ActiveGaugeInterval is how often the Redis blocker scans for live blocks
	// to publish the active-block gauge. Default 30s. Only used when the runtime
	// blocker is Redis-backed.
	ActiveGaugeInterval string `json:"active_gauge_interval" yaml:"active_gauge_interval"`
}

// GetRateLimitedDuration returns the 429 cool-down with default.
func (c RuntimeBlockConfig) GetRateLimitedDuration() string {
	if c.RateLimitedDuration == "" {
		return "5s"
	}
	return c.RateLimitedDuration
}

// GetUnauthorizedDuration returns the 401 cool-down with default.
func (c RuntimeBlockConfig) GetUnauthorizedDuration() string {
	if c.UnauthorizedDuration == "" {
		return "2m"
	}
	return c.UnauthorizedDuration
}

// GetServerErrorDuration returns the 5xx cool-down with default.
func (c RuntimeBlockConfig) GetServerErrorDuration() string {
	if c.ServerErrorDuration == "" {
		return "2m"
	}
	return c.ServerErrorDuration
}

// GetOverloadedDuration returns the 529 cool-down with default.
func (c RuntimeBlockConfig) GetOverloadedDuration() string {
	if c.OverloadedDuration == "" {
		return "30s"
	}
	return c.OverloadedDuration
}

// GetActiveGaugeInterval returns the active-block scan interval with default.
func (c RuntimeBlockConfig) GetActiveGaugeInterval() string {
	if c.ActiveGaugeInterval == "" {
		return "30s"
	}
	return c.ActiveGaugeInterval
}

type TokenRefreshConfig struct {
	Enabled                  bool   `json:"enabled" yaml:"enabled"`
	CheckIntervalMinutes     int    `json:"check_interval_minutes" yaml:"check_interval_minutes"`
	RefreshBeforeExpiryHours int    `json:"refresh_before_expiry_hours" yaml:"refresh_before_expiry_hours"`
	MaxRetries               int    `json:"max_retries" yaml:"max_retries"`
	RetryBackoffSeconds      int    `json:"retry_backoff_seconds" yaml:"retry_backoff_seconds"`
	TempUnschedDuration      string `json:"temp_unsched_duration" yaml:"temp_unsched_duration"`
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
	if c.TokenRefresh.CheckIntervalMinutes > 0 {
		return fmt.Sprintf("%dm", c.TokenRefresh.CheckIntervalMinutes)
	}
	if c.RefreshInterval == "" {
		return "10m"
	}
	return c.RefreshInterval
}

// GetRefreshLookahead returns the refresh lookahead window with default.
func (c HybridAdaptorConfig) GetRefreshLookahead() string {
	if c.TokenRefresh.RefreshBeforeExpiryHours > 0 {
		return fmt.Sprintf("%dh", c.TokenRefresh.RefreshBeforeExpiryHours)
	}
	if c.RefreshLookahead == "" {
		return "24h"
	}
	return c.RefreshLookahead
}

func (c HybridAdaptorConfig) GetTokenRefreshEnabled() bool {
	if c.TokenRefresh.Enabled {
		return true
	}
	return c.Enabled
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
