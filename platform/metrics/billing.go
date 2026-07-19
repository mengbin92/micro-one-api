package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Billing Performance Metrics

// BillingReserveDuration tracks quota reservation operation duration.
var BillingReserveDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "reserve_duration_seconds",
		Help:      "Quota reservation operation duration in seconds",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
	},
	[]string{"mode"}, // mode: sync, async
)

// BillingCommitDuration tracks quota commit operation duration.
var BillingCommitDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "commit_duration_seconds",
		Help:      "Quota commit operation duration in seconds",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
	},
	[]string{"mode"},
)

// BillingReleaseDuration tracks quota release operation duration.
var BillingReleaseDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "release_duration_seconds",
		Help:      "Quota release operation duration in seconds",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
	},
	[]string{},
)

// BillingSettlementLag tracks lag between async pre-check and settlement.
var BillingSettlementLag = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "settlement_lag_seconds",
		Help:      "Lag between async pre-check and settlement in seconds",
		Buckets:   []float64{0.1, 0.5, 1, 5, 10, 30, 60},
	},
)

// Quota Check Fallback Metrics

// QuotaCheckFallback counts fallback activations in quota checking.
var QuotaCheckFallback = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "quota_check_fallback_total",
		Help:      "Number of quota check fallbacks (sync→async or cache)",
	},
	[]string{"reason"}, // reason: service_unavailable, timeout, circuit_open
)

// QuotaCacheHits counts quota cache hits.
var QuotaCacheHits = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "quota_cache_hits_total",
		Help:      "Number of quota cache hits",
	},
	[]string{"level"}, // level: l1, l2
)

// QuotaCacheMisses counts quota cache misses.
var QuotaCacheMisses = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "quota_cache_misses_total",
		Help:      "Number of quota cache misses",
	},
	[]string{},
)

// Async Billing Queue Metrics

// AsyncBillingQueueSize tracks current async billing queue size.
var AsyncBillingQueueSize = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "async_queue_size",
		Help:      "Current number of items in async billing settlement queue",
	},
	[]string{},
)

// AsyncBillingSettlementDuration tracks async settlement operation duration.
var AsyncBillingSettlementDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "async_settlement_duration_seconds",
		Help:      "Async billing settlement operation duration",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
	},
	[]string{"status"},
)

// AsyncBillingFallbackToSync counts fallbacks from async to sync billing.
var AsyncBillingFallbackToSync = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "async_fallback_to_sync_total",
		Help:      "Number of fallbacks from async to sync billing (queue full)",
	},
	[]string{},
)

// AsyncBillingDroppedFlushes counts ledger entries dropped by the async
// batch writer (e.g. no ledger repo configured or persistence failed).
var AsyncBillingDroppedFlushes = prometheus.NewCounter(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "async_dropped_flushes_total",
		Help:      "Number of ledger entries dropped during async batch flush",
	},
)

// AsyncBillingMissingReservationID counts CommitQuota requests that took the
// async path without a reservation_id. The success path of CommitQuota
// requires a reservation id (it is the key the worker uses to run the commit
// pipeline); reaching the async branch without one is a client contract
// violation. We still enqueue with a synthetic correlation id so the relay is
// not blocked, but the counter makes the misbehaving caller visible.
var AsyncBillingMissingReservationID = prometheus.NewCounter(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "async_missing_reservation_id_total",
		Help:      "Number of async CommitQuota calls missing reservation_id",
	},
)

// Ledger and Reconciliation Metrics

// LedgerWriteDuration tracks ledger entry write duration.
var LedgerWriteDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "ledger_write_duration_seconds",
		Help:      "Ledger entry write operation duration",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
	},
	[]string{"status"},
)

// ReservationExpirationCount tracks expired reservation cleanup.
var ReservationExpirationCount = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "reservation_expirations_total",
		Help:      "Number of expired reservations cleaned up",
	},
	[]string{"status"},
)

// ReconciliationLaggedTransactions tracks transactions pending reconciliation.
var ReconciliationLaggedTransactions = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "reconciliation_lagged_transactions",
		Help:      "Number of transactions pending reconciliation",
	},
	[]string{},
)

// Quota Usage Metrics

// QuotaUsageCurrent tracks current quota usage by user.
var QuotaUsageCurrent = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "quota_usage_current",
		Help:      "Current quota usage in USD cents",
	},
	[]string{"user_group"},
)

// QuotaBalanceRemaining tracks remaining quota balance.
var QuotaBalanceRemaining = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "quota_balance_remaining",
		Help:      "Remaining quota balance in USD cents",
	},
	[]string{"user_group"},
)

// QuotaFrozenAmount tracks currently frozen quota (reserved but not committed).
var QuotaFrozenAmount = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "micro_one_api",
		Subsystem: "billing",
		Name:      "quota_frozen_amount",
		Help:      "Currently frozen quota amount in USD cents",
	},
	[]string{},
)
