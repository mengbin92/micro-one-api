package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"micro-one-api/internal/billing/service"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/pkg/xhttp"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// ServiceAuth creates a middleware that validates Bearer token against SERVICE_TOKEN env var.
// If SERVICE_TOKEN is not set, the middleware rejects all requests to protected endpoints.
func ServiceAuth(next http.HandlerFunc) http.HandlerFunc {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	return func(w http.ResponseWriter, r *http.Request) {
		if serviceToken == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "service token not configured"})
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid authorization header"})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(serviceToken)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid service token"})
			return
		}
		next(w, r)
	}
}

// NewHTTPServer wires HTTP transport for billing-service.
func NewHTTPServer(addr string, svc *service.BillingService) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)

	// Health and metrics (unauthenticated)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
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
