package biz

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotificationNotFound = errors.New("notification not found")
	ErrInvalidNotification  = errors.New("invalid notification")
)

const (
	NotifyTypeWebhook = "webhook"
	NotifyTypeEmail   = "email"
	NotifyTypeEvent   = "event"

	NotifyStatusPending = "pending"
	NotifyStatusSent    = "sent"
	NotifyStatusFailed  = "failed"
)

// Notification represents an outgoing notification.
type Notification struct {
	ID         int64
	Type       string // webhook, email, event
	Recipient  string
	Subject    string
	Content    string
	Status     string // pending, sent, failed
	RetryCount int
	CreatedAt  time.Time
	SentAt     time.Time
}

// NotifyRepo is the repository interface for notification persistence.
type NotifyRepo interface {
	Create(ctx context.Context, n *Notification) error
	Get(ctx context.Context, id int64) (*Notification, error)
	List(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*Notification, int64, error)
	ListPending(ctx context.Context, limit int32, maxRetry int) ([]*Notification, error)
	UpdateStatus(ctx context.Context, id int64, status string) error
	MarkFailed(ctx context.Context, id int64) error
	RecordFailure(ctx context.Context, id int64, maxRetry int) error
}

// NotifyUsecase implements business logic for notify-worker.
type NotifyUsecase struct {
	repo NotifyRepo
}

func NewNotifyUsecase(repo NotifyRepo) *NotifyUsecase {
	return &NotifyUsecase{repo: repo}
}

func (uc *NotifyUsecase) CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) (*Notification, error) {
	if notifyType == "" || recipient == "" {
		return nil, ErrInvalidNotification
	}
	n := &Notification{
		Type:      notifyType,
		Recipient: recipient,
		Subject:   subject,
		Content:   content,
		Status:    NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	if err := uc.repo.Create(ctx, n); err != nil {
		return nil, err
	}
	return n, nil
}

func (uc *NotifyUsecase) GetNotification(ctx context.Context, id int64) (*Notification, error) {
	return uc.repo.Get(ctx, id)
}

func (uc *NotifyUsecase) ListNotifications(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*Notification, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return uc.repo.List(ctx, page, pageSize, notifyType, status)
}

func (uc *NotifyUsecase) MarkSent(ctx context.Context, id int64) error {
	return uc.repo.UpdateStatus(ctx, id, NotifyStatusSent)
}

func (uc *NotifyUsecase) MarkFailed(ctx context.Context, id int64) error {
	return uc.repo.MarkFailed(ctx, id)
}

func (uc *NotifyUsecase) ListPending(ctx context.Context, limit int32, maxRetry int) ([]*Notification, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if maxRetry < 1 {
		maxRetry = 3
	}
	return uc.repo.ListPending(ctx, limit, maxRetry)
}

func (uc *NotifyUsecase) RecordFailure(ctx context.Context, id int64, maxRetry int) error {
	if maxRetry < 1 {
		maxRetry = 3
	}
	return uc.repo.RecordFailure(ctx, id, maxRetry)
}
