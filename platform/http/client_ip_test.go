package xhttp

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{name: "untrusted peer cannot spoof", remoteAddr: "203.0.113.9:1234", xff: "1.2.3.4", want: "203.0.113.9"},
		{name: "trusted proxy forwards client", remoteAddr: "10.0.0.4:1234", xff: "198.51.100.7", want: "198.51.100.7"},
		{name: "walks trusted chain from right", remoteAddr: "10.0.0.4:1234", xff: "192.0.2.8, 10.0.0.5", want: "192.0.2.8"},
		{name: "ignores spoofed left edge", remoteAddr: "10.0.0.4:1234", xff: "1.2.3.4, 198.51.100.7", want: "198.51.100.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", tt.xff)
			if got := ClientIP(req, trusted); got != tt.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
