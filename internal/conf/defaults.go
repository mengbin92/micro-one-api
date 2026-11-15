package conf

// SubscriptionConfig default methods.

// GetSubscriptionEnabled reports whether the subscription account enforcement is enabled.
func (c *Subscription) GetSubscriptionEnabled() bool {
	if c == nil {
		return false
	}
	return c.Enabled
}

// GetUserRPMLimit returns the user RPM limit.
func (c *Subscription) GetUserRPMLimit() int32 {
	if c == nil {
		return 0
	}
	return c.UserRpmLimit
}

// SessionStickyConfig default methods.

// GetSessionStickyEnabled reports whether session-account stickiness is enabled.
func (c *SessionSticky) GetSessionStickyEnabled() bool {
	if c == nil {
		return false
	}
	return c.Enabled
}

// RelayOrchestratorConfig default methods.

// GetRelayOrchestratorEnabled reports whether the orchestrator route is enabled.
func (c *RelayOrchestrator) GetRelayOrchestratorEnabled() bool {
	if c == nil {
		return false
	}
	return c.Enabled
}

// GetRelayOrchestratorFullRollout reports whether all staged-endpoint traffic
// routes to the orchestrator regardless of the token allowlist. It only takes
// effect together with GetRelayOrchestratorEnabled.
func (c *RelayOrchestrator) GetRelayOrchestratorFullRollout() bool {
	if c == nil {
		return false
	}
	return c.FullRollout
}

// ChannelCacheConfig default methods.

// GetChannelCacheEnabled reports whether the channel cache is enabled.
func (c *ChannelCache) GetChannelCacheEnabled() bool {
	if c == nil {
		return false
	}
	return c.Enabled
}

// HybridAdaptorConfig default methods.

// GetHybridAdaptorEnabled reports whether the hybrid adaptor path is enabled.
func (c *HybridAdaptor) GetHybridAdaptorEnabled() bool {
	if c == nil {
		return false
	}
	return c.Enabled
}

// GetTokenRefreshEnabled reports whether token refresh is enabled.
func (c *HybridAdaptor) GetTokenRefreshEnabled() bool {
	if c == nil {
		return false
	}
	if c.TokenRefresh != nil && c.TokenRefresh.Enabled {
		return true
	}
	return c.Enabled
}

// OpenaiWSConfig default methods.
// Note: These methods use "GetOpenAIWS..." prefix to avoid conflict with
// proto-generated getters (GetWriteTimeout, GetIdleTimeout, etc.).
// The proto getters return raw field values (empty string if unset), while these
// methods provide defaults. Callers should use these methods when defaults are needed.

// GetOpenAIWSWriteTimeout returns the write timeout with default fallback.
func (c *OpenaiWS) GetOpenAIWSWriteTimeout() string {
	if c == nil || c.WriteTimeout == "" {
		return "2m"
	}
	return c.WriteTimeout
}

// GetOpenAIWSIdleTimeout returns the idle timeout with default fallback.
func (c *OpenaiWS) GetOpenAIWSIdleTimeout() string {
	if c == nil || c.IdleTimeout == "" {
		return "5m"
	}
	return c.IdleTimeout
}

// GetOpenAIWSDialTimeout returns the dial timeout with default fallback.
func (c *OpenaiWS) GetOpenAIWSDialTimeout() string {
	if c == nil || c.DialTimeout == "" {
		return "30s"
	}
	return c.DialTimeout
}

// GetOpenAIWSFirstMessageTimeout returns the first-message timeout with default.
func (c *OpenaiWS) GetOpenAIWSFirstMessageTimeout() string {
	if c == nil || c.FirstMessageTimeout == "" {
		return "30s"
	}
	return c.FirstMessageTimeout
}

// GetOpenAIWSMaxConnsPerChannel returns the per-channel pool cap with default.
func (c *OpenaiWS) GetOpenAIWSMaxConnsPerChannel() int {
	if c == nil || c.MaxConnsPerChannel <= 0 {
		return 8
	}
	return int(c.MaxConnsPerChannel)
}

// GetOpenAIWSFailoverMaxSwitches returns the failover switch limit with default.
func (c *OpenaiWS) GetOpenAIWSFailoverMaxSwitches() int {
	if c == nil || c.FailoverMaxSwitches <= 0 {
		return 2
	}
	return int(c.FailoverMaxSwitches)
}

// GetOpenAIWSStickyTTL returns the sticky binding TTL with default.
func (c *OpenaiWS) GetOpenAIWSStickyTTL() string {
	if c == nil || c.StickyTtl == "" {
		return "1h"
	}
	return c.StickyTtl
}

// GetOpenAIWSDrainTimeout returns the graceful-drain timeout with default.
func (c *OpenaiWS) GetOpenAIWSDrainTimeout() string {
	if c == nil || c.DrainTimeout == "" {
		return "30s"
	}
	return c.DrainTimeout
}
