package server

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/app/billing/internal/service"
	xhttp "micro-one-api/platform/http"
	"micro-one-api/platform/metrics"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// ServiceAuth creates a middleware that validates Bearer token against SERVICE_TOKEN env var.
// If SERVICE_TOKEN is not set, the middleware rejects all requests to protected endpoints.
func ServiceAuth(next http.HandlerFunc) http.HandlerFunc {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	return func(w http.ResponseWriter, r *http.Request) {
		if serviceToken == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = jsonx.NewEncoder(w).Encode(map[string]string{"error": "service token not configured"})
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = jsonx.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid authorization header"})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(serviceToken)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = jsonx.NewEncoder(w).Encode(map[string]string{"error": "invalid service token"})
			return
		}
		next(w, r)
	}
}

// NewHTTPServer wires HTTP transport for billing-service.
func NewHTTPServer(addr string, svc *service.BillingService) *khttp.Server {
	srv := xhttp.NewServer(khttp.Address(addr))

	// Health and metrics (unauthenticated)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Protected reconciliation endpoint
	srv.HandleFunc("/v1/reconciliation", ServiceAuth(svc.HandleReconciliation))
	srv.HandleFunc("/api/v1/user/payments/alipay/notify", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleAlipayNotify(w, r)
	})
	srv.HandleFunc("/api/user/payments/alipay/notify", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleAlipayNotify(w, r)
	})

	return srv
}
