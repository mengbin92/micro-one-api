package data

import (
	"context"
	"errors"
	"time"

	"micro-one-api/internal/billing/biz"

	"gorm.io/gorm"
)

type reservationRepo struct {
	data *Data
}

func NewReservationRepo(data *Data) biz.ReservationRepo {
	return &reservationRepo{data: data}
}

func (r *reservationRepo) CreateReservation(ctx context.Context, reservation *biz.Reservation) error {
	model := &reservationModel{
		ReservationID: reservation.ReservationID,
		UserID:       reservation.UserID,
		RequestID:    reservation.RequestID,
		Amount:       reservation.Amount,
		Status:       reservation.Status,
		Model:        stringPtr(reservation.Model),
		ChannelID:    stringPtr(reservation.ChannelID),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		ExpiredAt:    timePtr(reservation.ExpiredAt),
	}

	if err := r.data.db.WithContext(ctx).Create(model).Error; err != nil {
		return err
	}

	return nil
}

func (r *reservationRepo) GetReservation(ctx context.Context, reservationID string) (*biz.Reservation, error) {
	var model reservationModel
	if err := r.data.db.WithContext(ctx).Where("reservation_id = ?", reservationID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrReservationNotFound
		}
		return nil, err
	}

	return &biz.Reservation{
		ReservationID: model.ReservationID,
		UserID:       model.UserID,
		RequestID:    model.RequestID,
		Amount:       model.Amount,
		Status:       model.Status,
		Model:        stringFromPtr(model.Model),
		ChannelID:    stringFromPtr(model.ChannelID),
		CreatedAt:    model.CreatedAt,
		UpdatedAt:    model.UpdatedAt,
		ExpiredAt:    timeFromPtr(model.ExpiredAt),
	}, nil
}

func (r *reservationRepo) FindByRequestID(ctx context.Context, requestID string) (*biz.Reservation, error) {
	var model reservationModel
	if err := r.data.db.WithContext(ctx).Where("request_id = ?", requestID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &biz.Reservation{
		ReservationID: model.ReservationID,
		UserID:       model.UserID,
		RequestID:    model.RequestID,
		Amount:       model.Amount,
		Status:       model.Status,
		Model:        stringFromPtr(model.Model),
		ChannelID:    stringFromPtr(model.ChannelID),
		CreatedAt:    model.CreatedAt,
		UpdatedAt:    model.UpdatedAt,
		ExpiredAt:    timeFromPtr(model.ExpiredAt),
	}, nil
}

func (r *reservationRepo) UpdateReservationStatus(ctx context.Context, reservationID string, status string) error {
	return r.data.db.WithContext(ctx).Model(&reservationModel{}).
		Where("reservation_id = ?", reservationID).
		Update("status", status).Error
}

func (r *reservationRepo) GetExpiredReservations(ctx context.Context) ([]*biz.Reservation, error) {
	var models []reservationModel
	now := time.Now()
	if err := r.data.db.WithContext(ctx).
		Where("status = ? AND expired_at < ?", "reserved", now).
		Find(&models).Error; err != nil {
		return nil, err
	}

	reservations := make([]*biz.Reservation, len(models))
	for i, model := range models {
		reservations[i] = &biz.Reservation{
			ReservationID: model.ReservationID,
			UserID:       model.UserID,
			RequestID:    model.RequestID,
			Amount:       model.Amount,
			Status:       model.Status,
			Model:        stringFromPtr(model.Model),
			ChannelID:    stringFromPtr(model.ChannelID),
			CreatedAt:    model.CreatedAt,
			UpdatedAt:    model.UpdatedAt,
			ExpiredAt:    timeFromPtr(model.ExpiredAt),
		}
	}

	return reservations, nil
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringFromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeFromPtr(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
