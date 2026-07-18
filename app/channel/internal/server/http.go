package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"micro-one-api/app/channel/internal/biz"
	channeloauth "micro-one-api/app/channel/internal/biz/oauth"
	"micro-one-api/platform/metrics"
	"micro-one-api/platform/http"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// NewHTTPServer wires HTTP transport for channel-service.
func NewHTTPServer(addr string, usecases ...*biz.ChannelUsecase) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)
	var uc *biz.ChannelUsecase
	if len(usecases) > 0 {
		uc = usecases[0]
	}
	oauthSvc := channeloauth.NewService(uc)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	registerOAuthRoutes(srv, oauthSvc)
	registerSelectorStatsRoute(srv, uc)
	return srv
}

func registerOAuthRoutes(srv *khttp.Server, oauthSvc *channeloauth.Service) {
	srv.HandleFunc("/api/v1/admin/accounts/subscription/oauth/claude/auth-url", oauthAuthURLHandler(oauthSvc, channeloauth.PlatformClaude))
	srv.HandleFunc("/api/v1/admin/accounts/subscription/oauth/claude/exchange", oauthExchangeHandler(oauthSvc, channeloauth.PlatformClaude))
	srv.HandleFunc("/api/v1/admin/accounts/subscription/oauth/codex/auth-url", oauthAuthURLHandler(oauthSvc, channeloauth.PlatformCodex))
	srv.HandleFunc("/api/v1/admin/accounts/subscription/oauth/codex/exchange", oauthExchangeHandler(oauthSvc, channeloauth.PlatformCodex))
}

type oauthAuthURLRequest struct {
	RedirectURI string `json:"redirect_uri"`
}

type oauthExchangeRequest struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	Models    string `json:"models"`
	Priority  int64  `json:"priority"`
	BaseURL   string `json:"base_url"`
	Metadata  string `json:"metadata"`
}

func oauthAuthURLHandler(oauthSvc *channeloauth.Service, platform string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req oauthAuthURLRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, err := oauthSvc.AuthURL(r.Context(), platform, channeloauth.AuthURLRequest{
			RedirectURI: req.RedirectURI,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"auth_url":   result.AuthURL,
			"session_id": result.SessionID,
			"state":      result.State,
			"expires_at": result.ExpiresAt,
		})
	}
}

func oauthExchangeHandler(oauthSvc *channeloauth.Service, platform string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req oauthExchangeRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		result, err := oauthSvc.Exchange(r.Context(), platform, channeloauth.ExchangeRequest{
			SessionID: req.SessionID,
			State:     req.State,
			Code:      req.Code,
			Name:      req.Name,
			Group:     req.Group,
			Models:    req.Models,
			Priority:  req.Priority,
			BaseURL:   req.BaseURL,
			Metadata:  req.Metadata,
		})
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, channeloauth.ErrInvalidSession) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"account_id": result.AccountID,
			"platform":   result.Platform,
			"metadata":   result.Metadata,
		})
	}
}

func decodeJSON(r *http.Request, dst interface{}) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// registerSelectorStatsRoute exposes the weighted selector's runtime stats as
// a read-only admin endpoint. It is the runtime-verification surface for
// Phase 2.2 (channel weighted selection): operators can curl it to confirm
// that selection actually flows through WeightedSelector (per-channel weight,
// current weight, inflight, p95 latency and error rate populated) rather than
// being bypassed.
//
// The endpoint is guarded by the shared ADMIN_TOKEN (constant-time compare),
// matching the admin-api guard. When ADMIN_TOKEN is unset the endpoint
// returns 503 so it cannot be used unauthenticated in misconfigured
// deployments.
func registerSelectorStatsRoute(srv *khttp.Server, uc *biz.ChannelUsecase) {
	srv.HandleFunc("/api/v1/admin/channels/selector/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if !authorizeAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin credentials"})
			return
		}
		if uc == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "channel usecase not wired"})
			return
		}
		writeJSON(w, http.StatusOK, selectorStatsPayload(uc.SelectorStats()))
	})
}

// authorizeAdmin validates the bearer token against the shared ADMIN_TOKEN
// using a constant-time comparison. Returns false when ADMIN_TOKEN is unset
// (fail-closed).
func authorizeAdmin(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
	if expected == "" {
		return false
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

// selectorStatsPayload shapes biz.ChannelStats into a JSON-friendly map keyed
// by channel id so the endpoint output is stable and self-describing.
func selectorStatsPayload(stats map[int64]biz.ChannelStats) map[string]interface{} {
	channels := make(map[int64]biz.ChannelStats, len(stats))
	for id, s := range stats {
		channels[id] = s
	}
	return map[string]interface{}{
		"channels": channels,
	}
}
