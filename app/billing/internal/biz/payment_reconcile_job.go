package biz

import (
	"context"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

// PaymentReconcileJob periodically checks a bounded set of pending provider
// orders, allowing lost callbacks to converge without waiting for a user read.
type PaymentReconcileJob struct {
	uc       *PaymentUsecase
	interval time.Duration
}

func NewPaymentReconcileJob(uc *PaymentUsecase, interval time.Duration) *PaymentReconcileJob {
	if interval <= 0 {
		interval = time.Minute
	}
	return &PaymentReconcileJob{uc: uc, interval: interval}
}

func (j *PaymentReconcileJob) Start(ctx context.Context) {
	if j == nil || j.uc == nil {
		return
	}
	_ = j.run(ctx)
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = j.run(ctx)
		}
	}
}

func (j *PaymentReconcileJob) run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	report, err := j.uc.ReconcilePendingOrders(ctx, 100)
	// Shutdown interrupts a scan; it is neither a completed failure nor recovery.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		metrics.PaymentReconcileRuns.WithLabelValues("error").Inc()
		metrics.PaymentReconcileFailed.Set(1)
		applogger.Log.Warn("payment pending reconciliation failed", zap.Error(err))
		return err
	}
	if report.QueryFailures > 0 {
		metrics.PaymentReconcileRuns.WithLabelValues("partial").Inc()
		metrics.PaymentReconcileFailed.Set(1)
		applogger.Log.Warn("payment pending order refreshes failed", zap.Int("scanned", report.Scanned), zap.Int("refresh_failures", report.QueryFailures))
	} else {
		metrics.PaymentReconcileRuns.WithLabelValues("success").Inc()
		metrics.PaymentReconcileFailed.Set(0)
	}
	return nil
}
