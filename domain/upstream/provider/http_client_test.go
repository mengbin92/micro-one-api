package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"testing"
)

func TestSSRFSafeDialerRejectsPrivateResolution(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "")
	dialCalled := false
	dialer := &ssrfSafeDialer{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("169.254.169.254")}}, nil
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("unexpected dial")
		},
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("DialContext() succeeded for mixed public/private DNS answer")
	}
	if dialCalled {
		t.Fatal("network dial occurred before all resolved addresses were validated")
	}
}

func TestSSRFSafeDialerPinsApprovedAddress(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "")
	wantErr := errors.New("stop after address capture")
	var dialled string
	dialer := &ssrfSafeDialer{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
		dialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialled = address
			return nil, wantErr
		},
	}
	_, err := dialer.DialContext(context.Background(), "tcp", "example.com:443")
	if !errors.Is(err, wantErr) {
		t.Fatalf("DialContext() error = %v, want sentinel", err)
	}
	if dialled != "93.184.216.34:443" {
		t.Fatalf("dialled address = %q, want DNS-pinned IP", dialled)
	}
}

func TestSSRFSafeDialerFallsBackAcrossApprovedAddresses(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "")
	wantErr := errors.New("last approved address failed")
	var dialled []string
	dialer := &ssrfSafeDialer{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("93.184.216.34")},
				{IP: net.ParseIP("93.184.216.35")},
			}, nil
		},
		dialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialled = append(dialled, address)
			if len(dialled) == 1 {
				return nil, errors.New("first approved address failed")
			}
			return nil, wantErr
		},
	}
	_, err := dialer.DialContext(context.Background(), "tcp", "example.com:443")
	if !errors.Is(err, wantErr) {
		t.Fatalf("DialContext() error = %v, want final sentinel", err)
	}
	want := []string{"93.184.216.34:443", "93.184.216.35:443"}
	if !slices.Equal(dialled, want) {
		t.Fatalf("dialled addresses = %v, want %v", dialled, want)
	}
}

func TestUpstreamRedirectPolicyRevalidatesDestination(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "")
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/metadata", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := upstreamRedirectPolicy(false)(req, nil); err == nil {
		t.Fatal("strict redirect policy accepted loopback destination")
	}
	if err := upstreamRedirectPolicy(false)(WithLocalNetworkAccess(req), nil); err != nil {
		t.Fatalf("explicit local channel redirect rejected: %v", err)
	}
}
