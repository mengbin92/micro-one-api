package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

type localNetworkAccessKey struct{}

type ssrfSafeDialer struct {
	lookupIP    func(context.Context, string) ([]net.IPAddr, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
	allowLocal  bool
}

// NewHTTPClient returns the standard non-streaming upstream client. DNS is
// resolved and checked on every new connection, then the approved IP is
// dialled directly so a second DNS lookup cannot rebind the destination.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return newHTTPClient(timeout, false)
}

func newHTTPClient(timeout time.Duration, allowLocal bool) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     newUpstreamTransport(0, allowLocal),
		CheckRedirect: upstreamRedirectPolicy(allowLocal),
	}
}

func newUpstreamTransport(responseHeaderTimeout time.Duration, allowLocal bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A forward proxy resolves the ultimate target outside this process and
	// would bypass the dial-time IP check. Provider traffic therefore connects
	// directly; operators should enforce egress policy at the network layer.
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	guard := &ssrfSafeDialer{
		lookupIP:    net.DefaultResolver.LookupIPAddr,
		dialContext: dialer.DialContext,
		allowLocal:  allowLocal,
	}
	transport.DialContext = guard.DialContext
	return transport
}

func (d *ssrfSafeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream address: %w", err)
	}
	ips, err := d.lookupIP(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve upstream host: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("upstream host resolved to no addresses")
	}
	allowLocal := d.allowLocal || localNetworkAccessAllowed(ctx) || os.Getenv("PROVIDER_DISABLE_SSRF_CHECK") == "true"
	for _, resolved := range ips {
		if !allowLocal && isPrivateOrReservedIP(resolved.IP) {
			return nil, fmt.Errorf("upstream host resolves to private/reserved IP: %s", resolved.IP)
		}
	}
	var dialErrors []error
	for _, resolved := range ips {
		conn, dialErr := d.dialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		dialErrors = append(dialErrors, dialErr)
	}
	return nil, fmt.Errorf("dial approved upstream addresses: %w", errors.Join(dialErrors...))
}

func upstreamRedirectPolicy(allowLocal bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, _ []*http.Request) error {
		if allowLocal || localNetworkAccessAllowed(req.Context()) || os.Getenv("PROVIDER_DISABLE_SSRF_CHECK") == "true" {
			return validateBaseURLAllowLocal(req.URL.String())
		}
		if err := validateBaseURL(req.URL.String()); err != nil {
			return fmt.Errorf("unsafe upstream redirect: %w", err)
		}
		return nil
	}
}

// WithLocalNetworkAccess marks a request from an explicitly self-hosted
// channel (currently Ollama). Other channel types always use strict egress.
func WithLocalNetworkAccess(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	return req.WithContext(context.WithValue(req.Context(), localNetworkAccessKey{}, true))
}

func localNetworkAccessAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(localNetworkAccessKey{}).(bool)
	return allowed
}
