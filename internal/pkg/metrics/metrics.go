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
	)
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
