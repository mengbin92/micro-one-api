package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Circuit Breaker Metrics

// CircuitBreakerState tracks the current state of circuit breakers.
// State values: 0=closed, 1=half-open, 2=open
var CircuitBreakerState = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "circuit_breaker_state",
		Help:      "Current state of the circuit breaker: 0=closed, 1=half-open, 2=open",
	},
	[]string{"service"}, // service: identity, channel, billing, log
)

// CircuitBreakerTrips counts the number of times circuit breakers have tripped.
var CircuitBreakerTrips = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "circuit_breaker_trips_total",
		Help:      "Total number of circuit breaker trips",
	},
	[]string{"service"},
)

// CircuitBreakerRequests counts total requests through the circuit breaker.
var CircuitBreakerRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "circuit_breaker_requests_total",
		Help:      "Total number of requests through the circuit breaker",
	},
	[]string{"service", "result"}, // result: success, failure, rejected
)

// CircuitBreakerFailures counts consecutive failures.
var CircuitBreakerFailures = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "circuit_breaker_failures_total",
		Help:      "Total number of failures counted by the circuit breaker",
	},
	[]string{"service"},
)

// Degradation Metrics

// DegradationLevel tracks the current degradation level.
// Level values: 0=none, 1=cached, 2=async, 3=minimal
var DegradationLevel = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "degradation_level",
		Help:      "Current degradation level: 0=none, 1=cached, 2=async, 3=minimal",
	},
	[]string{"service"},
)

// DegradationDuration tracks time spent in each degradation level.
var DegradationDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "degradation_duration_seconds",
		Help:      "Time spent in each degradation level",
		Buckets:   []float64{1, 10, 60, 300, 600, 1800, 3600}, // 1s to 1h
	},
	[]string{"level"},
)

// FallbackActivation counts fallback activations.
var FallbackActivation = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "resilience",
		Name:      "fallback_activations_total",
		Help:      "Total number of fallback activations",
	},
	[]string{"service", "strategy"}, // strategy: cache, async, noop
)

// Cache Metrics

// CacheHits counts cache hits by cache type and level.
var CacheHits = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "cache",
		Name:      "hits_total",
		Help:      "Total number of cache hits",
	},
	[]string{"cache", "level"}, // cache: auth, channel, quota; level: l1, l2
)

// CacheMisses counts cache misses.
var CacheMisses = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "cache",
		Name:      "misses_total",
		Help:      "Total number of cache misses",
	},
	[]string{"cache"},
)

// CacheEvictions counts cache evictions.
var CacheEvictions = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "cache",
		Name:      "evictions_total",
		Help:      "Total number of cache evictions",
	},
	[]string{"cache", "level"},
)

// CacheLatency tracks cache operation latency.
var CacheLatency = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "cache",
		Name:      "operation_duration_seconds",
		Help:      "Cache operation latency in seconds",
		Buckets:   []float64{0.00001, 0.00005, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05},
	},
	[]string{"cache", "operation", "level"}, // operation: get, set, del; level: l1, l2
)

// CacheSize tracks current cache size.
var CacheSize = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "cache",
		Name:      "size",
		Help:      "Current number of items in cache",
	},
	[]string{"cache", "level"},
)

// Relay Resilience Metrics

// RelayUpstreamDuration tracks time spent waiting for upstream provider.
var RelayUpstreamDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "upstream_duration_seconds",
		Help:      "Time spent waiting for upstream provider response",
		Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	},
	[]string{"provider", "model", "stream"},
)

// RelayRetryCount counts retry attempts.
var RelayRetryCount = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "retry_total",
		Help:      "Total number of retry attempts",
	},
	[]string{"endpoint", "reason"}, // reason: timeout, error_5xx, rate_limited
)

// RelayFailoverCount counts channel failover events.
var RelayFailoverCount = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "failover_total",
		Help:      "Total number of channel failover events",
	},
	[]string{"from_channel_type", "to_channel_type"},
)

// RelayOrchestratorDuration tracks full request orchestration time.
var RelayOrchestratorDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "relay",
		Name:      "orchestration_duration_seconds",
		Help:      "Full request orchestration duration (auth → select → reserve → forward → commit → log)",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
	},
	[]string{"endpoint", "degradation_level"},
)

// Service Dependency Health Metrics

// ServiceDependencyLatency tracks gRPC call latency to dependencies.
var ServiceDependencyLatency = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "dependency",
		Name:      "grpc_latency_seconds",
		Help:      "gRPC call latency to dependent services",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	},
	[]string{"service", "method", "status"},
)

// ServiceDependencyErrors tracks gRPC error rates.
var ServiceDependencyErrors = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "dependency",
		Name:      "grpc_errors_total",
		Help:      "Total number of gRPC errors to dependent services",
	},
	[]string{"service", "method", "error_code"},
)
