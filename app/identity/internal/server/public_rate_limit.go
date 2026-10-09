package server

import (
	"net/http"
	"strings"
	"time"

	"micro-one-api/app/identity/internal/biz"
)

func publicIdentityRateLimit(uc *biz.IdentityUsecase) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			public := path == "/api/user/register" || path == "/api/user/reset" || path == "/api/reset_password" || path == "/api/verification" || strings.HasPrefix(path, "/api/oauth/") || strings.HasPrefix(path, "/v1/oauth/")
			if public {
				allowed := uc.AllowPublicRequest(r.Context(), "ip:"+requestRemoteIP(r), 30, 10*time.Minute)
				if email := r.URL.Query().Get("email"); allowed && email != "" && (path == "/api/reset_password" || path == "/api/verification") {
					allowed = uc.AllowPublicRequest(r.Context(), "mail:"+strings.ToLower(strings.TrimSpace(email)), 3, 10*time.Minute)
				}
				if !allowed {
					w.Header().Set("Retry-After", "600")
					writeJSON(w, http.StatusTooManyRequests, apiResponse{Success: false, Message: "too many requests"})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
