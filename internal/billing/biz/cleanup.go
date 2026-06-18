package biz

import (
	"context"
	"time"

	"go.uber.org/zap"

	applogger "micro-one-api/internal/pkg/logger"
)

type CleanupJob struct {
	uc            *BillingUsecase
	checkInterval time.Duration
	stopChan      chan struct{}
}

func NewCleanupJob(uc *BillingUsecase, checkInterval time.Duration) *CleanupJob {
	return &CleanupJob{
		uc:            uc,
		checkInterval: checkInterval,
		stopChan:      make(chan struct{}),
	}
}

func (j *CleanupJob) Start(ctx context.Context) {
	ticker := time.NewTicker(j.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := j.cleanupExpiredReservations(ctx); err != nil {
				applogger.Log.Warn("cleanup expired reservations failed", zap.Error(err))
			}
		case <-ctx.Done():
			applogger.Log.Info("cleanup job stopped", zap.String("reason", "context canceled"))
			return
		case <-j.stopChan:
			applogger.Log.Info("cleanup job stopped", zap.String("reason", "stop requested"))
			return
		}
	}
}

func (j *CleanupJob) Stop() {
	close(j.stopChan)
}

func (j *CleanupJob) cleanupExpiredReservations(ctx context.Context) error {
	reservations, err := j.uc.reservationRepo.GetExpiredReservations(ctx)
	if err != nil {
		return err
	}

	for _, reservation := range reservations {
		if err := j.uc.ReleaseQuota(ctx, reservation.ReservationID, "reservation expired"); err != nil {
			applogger.Log.Warn("failed to release expired reservation", zap.String("reservation_id", reservation.ReservationID), zap.Error(err))
		} else {
			applogger.Log.Info("released expired reservation", zap.String("reservation_id", reservation.ReservationID))
		}

		if err := j.uc.reservationRepo.UpdateReservationStatus(ctx, reservation.ReservationID, ReservationStatusExpired); err != nil {
			applogger.Log.Warn("failed to update reservation status to expired", zap.String("reservation_id", reservation.ReservationID), zap.Error(err))
		}
	}

	return nil
}
