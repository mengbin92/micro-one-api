package server

import (
	"crypto/subtle"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	"net/http"
	"os"
	"strings"

	"micro-one-api/pkg/jsonx"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/log/internal/service"
	xhttp "micro-one-api/platform/http"
	"micro-one-api/platform/metrics"
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

// NewHTTPServer wires HTTP transport for log-service.
func NewHTTPServer(addr string, svc *service.LogService, identityClients ...identityv1.IdentityServiceClient) *khttp.Server {
	srv := xhttp.NewServer(khttp.Address(addr))
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
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv.HandleFunc("/v1/logs/export", authz.HTTPContext("/api.log.v1.LogService/ExportLogs", svc.HandleExportLogs))
	srv.HandleFunc("/v1/logs/purge", authz.HTTPContext("/api.log.v1.LogService/PurgeLogs", svc.HandlePurgeLogs))
	// Protected log endpoints
	srv.HandleFunc("/v1/selection-events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz.HTTPContext("/api.log.v1.LogService/ListSelectionAudit", svc.HandleListSelectionAudit)(w, r)
	}))
	srv.HandleFunc("/v1/logs", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authz.HTTPContext("/api.log.v1.LogService/ListLogs", svc.HandleListLogs)(w, r)
		case http.MethodPost:
			authz.HTTPContext("/api.log.v1.LogService/IngestLog", svc.HandleIngestLog)(w, r)
		case http.MethodDelete:
			authz.HTTPContext("/api.log.v1.LogService/DeleteLogs", svc.HandleDeleteLogs)(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}))
	srv.HandlePrefix("/v1/logs/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz.HTTPContext("/api.log.v1.LogService/GetLog", svc.HandleGetLog)(w, r)
	}))
	srv.HandleFunc("/api/log/self", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleOneAPIUserLogs(w, r.WithContext(authorization.WithCredential(authorization.WithExternal(r.Context()), r.Header.Get("Authorization"))), identityClient)
	})
	srv.HandleFunc("/api/log/self/search", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleOneAPIUserLogSearch(w, r.WithContext(authorization.WithCredential(authorization.WithExternal(r.Context()), r.Header.Get("Authorization"))), identityClient)
	})
	srv.HandleFunc("/api/log/self/stat", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleOneAPIUserLogStats(w, r.WithContext(authorization.WithCredential(authorization.WithExternal(r.Context()), r.Header.Get("Authorization"))), identityClient)
	})

	return srv
}
