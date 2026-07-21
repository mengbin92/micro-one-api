package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTPRequestsTotal counts total HTTP requests by service, method, path, and status code.
	HTTPRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"service", "method", "path", "status"},
	)

	// HTTPRequestDuration records HTTP request duration by service, method, and path.
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "micro_one_api",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"service", "method", "path"},
	)

	// GRPCRequestsTotal counts total gRPC requests by service, method, and status code.
	GRPCRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "grpc",
			Name:      "requests_total",
			Help:      "Total number of gRPC requests",
		},
		[]string{"service", "method", "status"},
	)

	// GRPCRequestDuration records gRPC request duration by service and method.
	GRPCRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "micro_one_api",
			Subsystem: "grpc",
			Name:      "request_duration_seconds",
			Help:      "gRPC request duration in seconds",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"service", "method"},
	)

	// ActiveRequests tracks currently in-flight requests.
	ActiveRequests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "micro_one_api",
			Subsystem: "http",
			Name:      "active_requests",
			Help:      "Number of currently active HTTP requests",
		},
		[]string{"service"},
	)

	// BillingReservationsTotal counts billing reservations by status.
	BillingReservationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "billing",
			Name:      "reservations_total",
			Help:      "Total number of billing reservations",
		},
		[]string{"status"},
	)

	// ChannelSelectionTotal counts channel selection attempts.
	ChannelSelectionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "channel",
			Name:      "selection_total",
			Help:      "Total channel selection attempts",
		},
		[]string{"model", "status"},
	)

	// UsageLogIngestTotal counts usage log ingestion attempts by status.
	UsageLogIngestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "log",
			Name:      "usage_ingest_total",
			Help:      "Total usage log ingestion attempts",
		},
		[]string{"status"},
	)

	// ReconciliationRunsTotal counts reconciliation job executions by status.
	ReconciliationRunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "billing",
			Name:      "reconciliation_runs_total",
			Help:      "Total number of billing reconciliation job executions",
		},
		[]string{"status"},
	)

	// ReconciliationRunDuration records reconciliation job duration by status.
	ReconciliationRunDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "micro_one_api",
			Subsystem: "billing",
			Name:      "reconciliation_run_duration_seconds",
			Help:      "Billing reconciliation job duration in seconds",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"status"},
	)

	// ReconciliationDiscrepanciesTotal counts discrepancies found by type.
	ReconciliationDiscrepanciesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "billing",
			Name:      "reconciliation_discrepancies_total",
			Help:      "Total number of reconciliation discrepancies found",
		},
		[]string{"type"},
	)

	// ChannelHealthCheckRunsTotal counts monitor-worker channel health check sweeps.
	ChannelHealthCheckRunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "monitor",
			Name:      "channel_health_check_runs_total",
			Help:      "Total number of channel health check sweeps",
		},
		[]string{"status"},
	)

	// ChannelHealthCheckRunDuration records channel health check sweep duration.
	ChannelHealthCheckRunDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "micro_one_api",
			Subsystem: "monitor",
			Name:      "channel_health_check_run_duration_seconds",
			Help:      "Channel health check sweep duration in seconds",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"status"},
	)

	// ChannelHealthProbeTotal counts individual channel health probes by status and failure reason.
	ChannelHealthProbeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "micro_one_api",
			Subsystem: "monitor",
			Name:      "channel_health_probe_total",
			Help:      "Total number of individual channel health probes",
		},
		[]string{"status", "reason"},
	)

	// ChannelHealthProbeDuration records individual channel health probe duration by status.
	ChannelHealthProbeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "micro_one_api",
			Subsystem: "monitor",
			Name:      "channel_health_probe_duration_seconds",
			Help:      "Individual channel health probe duration in seconds",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"status"},
	)
)

func init() {
	prometheus.MustRegister(
		HTTPRequestTotal,
		HTTPRequestDuration,
		GRPCRequestTotal,
		GRPCRequestDuration,
		ActiveRequests,
		BillingReservationsTotal,
		ChannelSelectionTotal,
		UsageLogIngestTotal,
		ReconciliationRunsTotal,
		ReconciliationRunDuration,
		ReconciliationDiscrepanciesTotal,
		ChannelHealthCheckRunsTotal,
		ChannelHealthCheckRunDuration,
		ChannelHealthProbeTotal,
		ChannelHealthProbeDuration,
	)

	// Register resilience metrics
	prometheus.MustRegister(
		CircuitBreakerState,
		CircuitBreakerTrips,
		CircuitBreakerRequests,
		CircuitBreakerFailures,
		DegradationLevel,
		DegradationDuration,
		FallbackActivation,
		CacheHits,
		CacheMisses,
		CacheEvictions,
		CacheLatency,
		CacheSize,
		RelayUpstreamDuration,
		RelayRetryCount,
		RelayFailoverCount,
		RelayOrchestratorDuration,
		ServiceDependencyLatency,
		ServiceDependencyErrors,
	)

	// Register billing metrics
	prometheus.MustRegister(
		BillingReserveDuration,
		BillingCommitDuration,
		BillingReleaseDuration,
		BillingSettlementLag,
		QuotaCheckFallback,
		QuotaCacheHits,
		QuotaCacheMisses,
		AsyncBillingQueueSize,
		AsyncBillingSettlementDuration,
		AsyncBillingFallbackToSync,
		AsyncBillingMissingReservationID,
		AsyncBillingDroppedFlushes,
		LedgerWriteDuration,
		ReservationExpirationCount,
		ReconciliationLaggedTransactions,
		QuotaUsageCurrent,
		QuotaBalanceRemaining,
		QuotaFrozenAmount,
	)

	// Register subscription system metrics
	prometheus.MustRegister(
		SubscriptionQuotaChecksTotal,
		SubscriptionUsageRecordsTotal,
		SubscriptionPriorityReservationsTotal,
		NegativeBalanceTotal,
		OverdueReceivablesTotal,
		RelaySubscriptionAdaptorRequestsTotal,
		RelaySubscriptionFailoverTotal,
		RelaySubscriptionStickyTotal,
		RelayRuntimeBlocksTotal,
		RelayRuntimeBlockActive,
		RelayAccountConcurrencyFallbackTotal,
		RelayAccountRPMFallbackTotal,
		RelayAccountPoolChecksTotal,
		RelayUpstreamPassthroughTotal,
		RelayCodexQuotaSnapshotsTotal,
		RelayCodexQuotaUsedPercent,
		SubscriptionAccountQuotaResetsTotal,
		SubscriptionAccountQuotaResetScanDuration,
		SubscriptionAccountRecoveriesTotal,
		SubscriptionQuotaProbeTotal,
		SubscriptionAccountRecoveryScanDuration,
		SubscriptionQuotaAlertsTotal,
	)
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
