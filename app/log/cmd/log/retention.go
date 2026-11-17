package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	"micro-one-api/app/log/internal/biz"
	applogger "micro-one-api/platform/logging"
)

const observationEvidenceRetention = 7 * 24 * time.Hour

func startLogRetentionCleanup(uc *biz.LogUsecase, observationUC *biz.UsageObservationUsecase, retentionDays int) func() {
	if retentionDays <= 0 && observationUC == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	runCleanup := func() {
		defer func() {
			if cause := recover(); cause != nil {
				applogger.Log.Error("log retention cleanup panicked", zap.Any("cause", cause))
			}
		}()
		if retentionDays > 0 {
			deleted, err := uc.CleanupExpiredLogs(ctx, retentionDays, time.Now())
			if err != nil {
				applogger.Log.Warn("log retention cleanup failed", zap.Int("retention_days", retentionDays), zap.Error(err))
			} else if deleted > 0 {
				applogger.Log.Info("log retention cleanup completed", zap.Int("retention_days", retentionDays), zap.Int64("deleted", deleted))
			}
		}
		// Observation evidence has its own retention (>= 7 days) and never
		// shares the consumption-log deletion path.
		if observationUC != nil {
			if observations, err := observationUC.CleanupObservations(ctx, time.Now().Add(-observationEvidenceRetention)); err != nil {
				applogger.Log.Warn("usage observation retention cleanup failed", zap.Error(err))
			} else if observations > 0 {
				applogger.Log.Info("usage observation retention cleanup completed", zap.Int64("deleted", observations))
			}
		}
	}
	go func() {
		runCleanup()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runCleanup()
			case <-ctx.Done():
				return
			}
		}
	}()
	return cancel
}
