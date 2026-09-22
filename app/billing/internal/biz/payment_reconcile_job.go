package biz

import (
	"context"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
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
	report, err := j.uc.ReconcilePendingOrders(ctx, 100)
	if err != nil {
		applogger.Log.Warn("payment pending reconciliation failed", zap.Error(err))
		return err
	}
	if report.QueryFailures > 0 {
		applogger.Log.Warn("payment provider status queries failed", zap.Int("scanned", report.Scanned), zap.Int("query_failures", report.QueryFailures))
	}
	return nil
}
