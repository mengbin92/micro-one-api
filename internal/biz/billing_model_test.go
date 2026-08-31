package biz

import "testing"

func TestBillingModelForSource_Requested(t *testing.T) {
	got := BillingModelForSource("requested", "gpt-4o", "gpt-4o-2024-08-06", "gpt-4o-2024-08-06")
	if got != "gpt-4o" {
		t.Fatalf("requested source must use client model, got %q", got)
	}
}

func TestBillingModelForSource_StripsExtendedContextSuffix(t *testing.T) {
	got := BillingModelForSource("requested", "glm-5.3[1M]", "", "")
	if got != "glm-5.3" {
		t.Fatalf("extended-context hint must not become a billing model, got %q", got)
	}
}

func TestBillingModelForSource_Upstream(t *testing.T) {
	got := BillingModelForSource("upstream", "gpt-4o", "gpt-4o-2024-08-06", "gpt-4o-2024-08-06")
	if got != "gpt-4o-2024-08-06" {
		t.Fatalf("upstream source must use resolved/upstream model, got %q", got)
	}
	// Empty upstream falls back to client.
	got = BillingModelForSource("upstream", "gpt-4o", "", "")
	if got != "gpt-4o" {
		t.Fatalf("empty upstream must fall back to client, got %q", got)
	}
}

func TestBillingModelForSource_ChannelMapped(t *testing.T) {
	got := BillingModelForSource("channel_mapped", "claude-sonnet-4-5", "claude-sonnet-4", "claude-sonnet-4")
	if got != "claude-sonnet-4" {
		t.Fatalf("channel_mapped must use mapped name, got %q", got)
	}
	// Fall back to resolved then client when upstream empty.
	got = BillingModelForSource("channel_mapped", "claude-sonnet-4-5", "claude-sonnet-4", "")
	if got != "claude-sonnet-4" {
		t.Fatalf("channel_mapped empty upstream must fall back to resolved, got %q", got)
	}
	got = BillingModelForSource("channel_mapped", "claude-sonnet-4-5", "", "")
	if got != "claude-sonnet-4-5" {
		t.Fatalf("channel_mapped empty resolved must fall back to client, got %q", got)
	}
}

func TestBillingModelForSource_DefaultUpstream(t *testing.T) {
	// Empty/unknown source must default to upstream (true legacy): pre-P3 #6
	// every reserveQuota call site passed plan.ResolvedModel (the upstream
	// name). Defaulting to requested would silently change the billing key
	// from the upstream name to the client name on any deployment that
	// upgrades without setting BILLING_MODEL_SOURCE.
	got := BillingModelForSource("", "gpt-4o", "gpt-4o-2024-08-06", "gpt-4o-2024-08-06")
	if got != "gpt-4o-2024-08-06" {
		t.Fatalf("empty source must default to upstream, got %q", got)
	}
	// Empty upstream falls back to client when source is unset.
	got = BillingModelForSource("", "gpt-4o", "gpt-4o-2024-08-06", "")
	if got != "gpt-4o" {
		t.Fatalf("empty source + empty upstream must fall back to client, got %q", got)
	}
}
