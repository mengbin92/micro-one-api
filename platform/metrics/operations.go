package metrics

import "github.com/prometheus/client_golang/prometheus"

var NotificationDelivery = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "micro_one_api_notification_delivery_total", Help: "Notification delivery attempts; queued is not delivered.",
}, []string{"result"})

var AccountProbeTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "micro_one_api_account_probe_tokens_total", Help: "Reported upstream tokens spent on account probes, excluded from user billing.",
}, []string{"platform", "bucket"})

var AccountProbeUsage = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "micro_one_api_account_probe_usage_total", Help: "Account probe responses with reported or missing usage.",
}, []string{"platform", "result"})

var LoginLimiterDegraded = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "micro_one_api_identity_login_limiter_degraded_total", Help: "Shared login limiter failures; read/write failures fall back to per-process counters.",
}, []string{"operation"})

func init() {
	prometheus.MustRegister(NotificationDelivery, AccountProbeTokens, AccountProbeUsage, LoginLimiterDegraded)
	for _, operation := range []string{"read", "write", "clear"} {
		LoginLimiterDegraded.WithLabelValues(operation)
	}
}
