package server

import (
	"net/http"
	"strconv"

	"go.uber.org/zap"

	applogger "micro-one-api/internal/pkg/logger"
	"micro-one-api/internal/pkg/metrics"
)

const defaultSubscriptionEstimatedCostUSD = 0.01

// SubscriptionQuotaMiddleware returns the optional subscription quota
// middleware. It is a no-op unless SetSubscriptionUsecase was called.
func (s *HTTPServer) SubscriptionQuotaMiddleware(next http.Handler) http.Handler {
	return s.withSubscriptionQuotaCheck(next)
}

func (s *HTTPServer) withSubscriptionQuotaCheck(next http.Handler) http.Handler {
	if s == nil || s.subscriptionUsecase == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := bearerTokenFromRequest(r)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		auth, err := s.getAuthSnapshot(r.Context(), token)
		if err != nil || auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		estimatedCost := subscriptionEstimatedCostFromHeader(r)
		result, err := s.subscriptionUsecase.CheckQuota(r.Context(), auth.GetUserId(), estimatedCost)
		if err != nil {
			metrics.SubscriptionQuotaChecksTotal.WithLabelValues("error").Inc()
			if applogger.Log != nil {
				applogger.Log.Warn("subscription quota check failed", zap.Int64("user_id", auth.GetUserId()), zap.Error(err))
			}
			next.ServeHTTP(w, r)
			return
		}
		if result == nil || result.Allowed {
			metrics.SubscriptionQuotaChecksTotal.WithLabelValues("allowed").Inc()
			next.ServeHTTP(w, r)
			return
		}
		metrics.SubscriptionQuotaChecksTotal.WithLabelValues("rejected").Inc()
		w.Header().Set("Retry-After", "60")
		s.writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"error": map[string]interface{}{
				"message": "subscription quota exceeded",
				"reasons": result.Reasons,
			},
		})
	})
}

func subscriptionEstimatedCostFromHeader(r *http.Request) float64 {
	if r == nil {
		return defaultSubscriptionEstimatedCostUSD
	}
	raw := r.Header.Get("X-Estimated-Cost-USD")
	if raw == "" {
		return defaultSubscriptionEstimatedCostUSD
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return defaultSubscriptionEstimatedCostUSD
	}
	return value
}
