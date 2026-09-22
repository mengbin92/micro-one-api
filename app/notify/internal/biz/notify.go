package biz

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotificationNotFound  = errors.New("notification not found")
	ErrInvalidNotification   = errors.New("invalid notification")
	ErrNotificationLeaseLost = errors.New("notification processing lease lost")
)

const (
	NotifyTypeWebhook  = "webhook"
	NotifyTypeEmail    = "email"
	NotifyTypeEvent    = "event"
	NotifyTypeWeCom    = "wecom"
	NotifyTypeDingTalk = "dingtalk"
	NotifyTypeFeishu   = "feishu"
	NotifyTypeSlack    = "slack"

	NotifyStatusPending    = "pending"
	NotifyStatusProcessing = "processing"
	NotifyStatusSent       = "sent"
	NotifyStatusFailed     = "failed"
)

// Notification represents an outgoing notification.
type Notification struct {
	ID           int64
	Type         string // webhook, email, event
	Recipient    string
	Subject      string
	Content      string
	Status       string // pending, sent, failed
	RetryCount   int
	LastError    string
	ProcessingAt time.Time
	CreatedAt    time.Time
	SentAt       time.Time
}

// NotifyRepo is the repository interface for notification persistence.
type NotifyRepo interface {
	Create(ctx context.Context, n *Notification) error
	Get(ctx context.Context, id int64) (*Notification, error)
	List(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*Notification, int64, error)
	ListPending(ctx context.Context, limit int32, maxRetry int) ([]*Notification, error)
	UpdateStatus(ctx context.Context, id int64, status string) error
	MarkFailed(ctx context.Context, id int64) error
	RecordFailure(ctx context.Context, id int64, maxRetry int, lastError string) error
}

// NotificationClaimer is an optional repository capability. Claiming is a
// compare-and-set transition so two notify workers cannot send the same row at
// the same time. Repositories that do not implement it retain the old
// single-worker behavior for tests and in-memory deployments.
type NotificationClaimer interface {
	ClaimPending(ctx context.Context, limit int32, maxRetry int) ([]*Notification, error)
	RecoverProcessing(ctx context.Context) error
}

// NotificationLeaseCompleter applies the delivery result only while the
// caller still owns the processing lease. This prevents a worker that resumed
// after lease expiry from overwriting the result written by a newer worker.
type NotificationLeaseCompleter interface {
	CompleteProcessing(ctx context.Context, id int64, processingAt time.Time, status string, maxRetry int, lastError string) error
}

// NotifyUsecase implements business logic for notify-worker.
type NotifyUsecase struct {
	repo NotifyRepo
}

func NewNotifyUsecase(repo NotifyRepo) *NotifyUsecase {
	return &NotifyUsecase{repo: repo}
}

func (uc *NotifyUsecase) CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) (*Notification, error) {
	if notifyType == "" {
		return nil, ErrInvalidNotification
	}
	if recipient == "" && notifyType == NotifyTypeEmail {
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
	if claimer, ok := uc.repo.(NotificationClaimer); ok {
		return claimer.ClaimPending(ctx, limit, maxRetry)
	}
	return uc.repo.ListPending(ctx, limit, maxRetry)
}

func (uc *NotifyUsecase) RecoverProcessing(ctx context.Context) error {
	if claimer, ok := uc.repo.(NotificationClaimer); ok {
		return claimer.RecoverProcessing(ctx)
	}
	return nil
}

func (uc *NotifyUsecase) RecordFailure(ctx context.Context, id int64, maxRetry int, lastError string) error {
	if maxRetry < 1 {
		maxRetry = 3
	}
	return uc.repo.RecordFailure(ctx, id, maxRetry, lastError)
}

func (uc *NotifyUsecase) CompleteProcessing(ctx context.Context, n *Notification, status string, maxRetry int, lastError string) error {
	if n == nil {
		return ErrNotificationNotFound
	}
	if completer, ok := uc.repo.(NotificationLeaseCompleter); ok {
		return completer.CompleteProcessing(ctx, n.ID, n.ProcessingAt, status, maxRetry, lastError)
	}
	if status == NotifyStatusSent {
		return uc.MarkSent(ctx, n.ID)
	}
	return uc.RecordFailure(ctx, n.ID, maxRetry, lastError)
}
