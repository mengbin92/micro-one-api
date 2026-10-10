package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestUpstreamRedirectPolicyPreservesOrigin(t *testing.T) {
	for _, destination := range []struct {
		origin  string
		url     string
		allowed bool
	}{
		{"https://api.example.com/request", "https://API.example.com/other", true},
		{"https://api.example.com/request", "https://api.example.com:443/other", true},
		{"https://api.example.com:443/request", "https://api.example.com/other", true},
		{"http://api.example.com/request", "http://api.example.com:80/other", true},
		{"http://api.example.com:80/request", "http://api.example.com/other", true},
		{"https://api.example.com/request", "https://cdn.api.example.com/other", false},
		{"https://api.example.com/request", "https://api.example.com:8443/other", false},
		{"https://api.example.com/request", "http://api.example.com/other", false},
	} {
		origin, err := http.NewRequest(http.MethodPost, destination.origin, nil)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, destination.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = upstreamRedirectPolicy(true)(req, []*http.Request{origin})
		if (err == nil) != destination.allowed {
			t.Fatalf("redirect to %q error = %v, allowed = %v", destination.url, err, destination.allowed)
		}
	}
}

func TestUpstreamHTTPClientsBoundRedirects(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[streaming], func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if hits <= 12 {
					http.Redirect(w, r, "/loop", http.StatusFound)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client := newHTTPClient(time.Second, true)
			if streaming {
				client = newStreamHTTPClientWithLocalAccess(time.Second, true)
			}
			resp, err := client.Get(server.URL)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatalf("redirect chain completed after %d requests; want redirect-limit error", hits)
			}
			if hits > 10 {
				t.Fatalf("followed %d requests; want at most 10", hits)
			}
		})
	}
}

func TestUpstreamHTTPClientsRejectCrossOriginRedirects(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[streaming], func(t *testing.T) {
			var forwarded atomic.Int32
			var requests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				for _, key := range []string{"x-api-key", "api-key", "x-goog-api-key"} {
					if r.Header.Get(key) != "" {
						forwarded.Add(1)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
			}))
			defer origin.Close()
			client := newHTTPClient(time.Second, true)
			if streaming {
				client = newStreamHTTPClientWithLocalAccess(time.Second, true)
			}
			req, err := http.NewRequest(http.MethodPost, origin.URL, strings.NewReader("private prompt"))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"x-api-key", "api-key", "x-goog-api-key"} {
				req.Header.Set(key, "upstream-secret")
			}
			resp, err := client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if forwarded.Load() != 0 {
				t.Fatalf("cross-origin redirect forwarded %d credential headers", forwarded.Load())
			}
			if requests.Load() != 0 {
				t.Fatal("cross-origin redirect forwarded the private request body")
			}
			if err == nil {
				t.Fatal("cross-origin redirect must be rejected")
			}
		})
	}
}
