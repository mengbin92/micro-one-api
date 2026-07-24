package biz

import "testing"

func TestBillingModelForSource_Requested(t *testing.T) {
	got := BillingModelForSource("requested", "gpt-4o", "gpt-4o-2024-08-06", "gpt-4o-2024-08-06")
	if got != "gpt-4o" {
		t.Fatalf("requested source must use client model, got %q", got)
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

func TestBillingModelForSource_DefaultRequested(t *testing.T) {
	got := BillingModelForSource("", "gpt-4o", "gpt-4o-2024-08-06", "gpt-4o-2024-08-06")
	if got != "gpt-4o" {
		t.Fatalf("empty source must default to requested/client, got %q", got)
	}
}
