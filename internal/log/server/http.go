package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	khttp "github.com/go-kratos/kratos/v2/transport/http"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/log/service"
	"micro-one-api/internal/pkg/metrics"
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

// NewHTTPServer wires HTTP transport for log-service.
func NewHTTPServer(addr string, svc *service.LogService, identityClients ...identityv1.IdentityServiceClient) *khttp.Server {
	srv := khttp.NewServer(
		khttp.Address(addr),
	)
	var identityClient identityv1.IdentityServiceClient
	if len(identityClients) > 0 {
		identityClient = identityClients[0]
	}

	// Health and metrics (unauthenticated)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Protected log endpoints
	srv.HandleFunc("/v1/logs", ServiceAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			svc.HandleListLogs(w, r)
		case http.MethodPost:
			svc.HandleIngestLog(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}))
	srv.HandleFunc("/v1/logs/", ServiceAuth(func(w http.ResponseWriter, r *http.Request) {
		svc.HandleGetLog(w, r)
	}))
	srv.HandleFunc("/api/log/self", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleOneAPIUserLogs(w, r, identityClient)
	})
	srv.HandleFunc("/api/log/self/search", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleOneAPIUserLogSearch(w, r, identityClient)
	})
	srv.HandleFunc("/api/log/self/stat", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleOneAPIUserLogStats(w, r, identityClient)
	})

	return srv
}
