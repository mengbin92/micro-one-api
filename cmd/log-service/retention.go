package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	"micro-one-api/internal/log/biz"
	applogger "micro-one-api/internal/pkg/logger"
)

func startLogRetentionCleanup(uc *biz.LogUsecase, retentionDays int) func() {
	if retentionDays <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	runCleanup := func() {
		deleted, err := uc.CleanupExpiredLogs(ctx, retentionDays, time.Now())
		if err != nil {
			applogger.Log.Warn("log retention cleanup failed", zap.Int("retention_days", retentionDays), zap.Error(err))
			return
		}
		if deleted > 0 {
			applogger.Log.Info("log retention cleanup completed", zap.Int("retention_days", retentionDays), zap.Int64("deleted", deleted))
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
