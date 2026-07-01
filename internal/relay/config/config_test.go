package config

import "testing"

func TestSubscriptionConfigDefaultDisabled(t *testing.T) {
	var cfg Config
	if cfg.Subscription.GetSubscriptionEnabled() {
		t.Fatal("subscription should be disabled by default")
	}
}

func TestSubscriptionConfigEnabled(t *testing.T) {
	cfg := Config{Subscription: SubscriptionConfig{Enabled: true}}
	if !cfg.Subscription.GetSubscriptionEnabled() {
		t.Fatal("subscription should be enabled")
	}
}

func TestTokenRefreshConfigDefaultsToHybridFlag(t *testing.T) {
	var cfg HybridAdaptorConfig
	if cfg.GetTokenRefreshEnabled() {
		t.Fatal("token refresh should be disabled when hybrid adaptor is disabled")
	}
	cfg.Enabled = true
	if !cfg.GetTokenRefreshEnabled() {
		t.Fatal("token refresh should follow hybrid adaptor enabled by default")
	}
}

func TestTokenRefreshNestedConfigOverridesLegacyDurations(t *testing.T) {
	cfg := HybridAdaptorConfig{
		RefreshInterval:  "10m",
		RefreshLookahead: "24h",
		TokenRefresh: TokenRefreshConfig{
			Enabled:                  true,
			CheckIntervalMinutes:     5,
			RefreshBeforeExpiryHours: 12,
		},
	}
	if got := cfg.GetRefreshInterval(); got != "5m" {
		t.Fatalf("refresh interval = %q, want 5m", got)
	}
	if got := cfg.GetRefreshLookahead(); got != "12h" {
		t.Fatalf("refresh lookahead = %q, want 12h", got)
	}
	if !cfg.GetTokenRefreshEnabled() {
		t.Fatal("explicit token refresh flag should enable refresh")
	}
}
