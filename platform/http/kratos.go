package xhttp

import (
	"net/http"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

const (
	defaultReadHeaderTimeout = 10 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultMaxHeaderBytes    = 1 << 20
)

// NewServer creates a Kratos HTTP server with safe fallback handlers and
// transport-level limits. WriteTimeout is intentionally left unset because
// relay responses may be healthy long-lived SSE or WebSocket streams.
func NewServer(opts ...khttp.ServerOption) *khttp.Server {
	srv := khttp.NewServer(SafeKratosServerOptions(opts...)...)
	srv.ReadHeaderTimeout = defaultReadHeaderTimeout
	srv.ReadTimeout = defaultReadTimeout
	srv.IdleTimeout = defaultIdleTimeout
	srv.MaxHeaderBytes = defaultMaxHeaderBytes
	return srv
}

// SafeKratosServerOptions avoids the Kratos v3.0.0 fallback to
// http.DefaultServeMux for unmatched routes and unsupported methods.
func SafeKratosServerOptions(opts ...khttp.ServerOption) []khttp.ServerOption {
	safeOpts := []khttp.ServerOption{
		khttp.NotFoundHandler(http.NotFoundHandler()),
		khttp.MethodNotAllowedHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		})),
	}
	return append(safeOpts, opts...)
}
