package biz

import (
	"context"
	"errors"
	"micro-one-api/domain/authorization"
	"strings"
	"time"
)

var ErrNotificationConflict = errors.New("notification revision conflict")

// NotificationRule governs delivery routing for a named internal event.
// Its resource ID belongs to rules.update/test, separate from notification IDs.
type NotificationRule struct {
	ID                           int64
	Name, Event, Type, Recipient string
	Enabled                      bool
	Revision                     uint64
}
type NotificationManagementRepo interface {
	Acknowledge(context.Context, int64, uint64) (*Notification, error)
	ListRules(context.Context) ([]*NotificationRule, error)
	SaveRule(context.Context, *NotificationRule) error
	TestRule(context.Context, int64) (*Notification, error)
	EventRules(context.Context, string) ([]*NotificationRule, error)
}

func (uc *NotifyUsecase) managementRepo() (NotificationManagementRepo, error) {
	r, ok := uc.repo.(NotificationManagementRepo)
	if !ok {
		return nil, ErrInvalidNotification
	}
	return r, nil
}
func (uc *NotifyUsecase) AcknowledgeNotification(ctx context.Context, id int64, revision uint64, reason string) (*Notification, error) {
	ctx, err := uc.authorizeManagement(ctx, "notify.notification.acknowledge")
	if err != nil {
		return nil, err
	}
	if id <= 0 || revision == 0 || strings.TrimSpace(reason) == "" {
		return nil, ErrInvalidNotification
	}
	if err := authorization.Require(ctx, "notify.notification.acknowledge", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
		return nil, err
	}
	r, err := uc.managementRepo()
	if err != nil {
		return nil, err
	}
	return r.Acknowledge(authorization.WithWriteReason(ctx, reason), id, revision)
}
func (uc *NotifyUsecase) ListNotificationRules(ctx context.Context) ([]*NotificationRule, error) {
	ctx, err := uc.authorizeManagement(ctx, "notify.notification.rules.update")
	if err != nil {
		return nil, err
	}
	r, err := uc.managementRepo()
	if err != nil {
		return nil, err
	}
	return r.ListRules(ctx)
}
func (uc *NotifyUsecase) SaveNotificationRule(ctx context.Context, rule *NotificationRule, reason string) error {
	ctx, err := uc.authorizeManagement(ctx, "notify.notification.rules.update")
	if err != nil {
		return err
	}
	if rule == nil || rule.ID <= 0 || rule.Name == "" || rule.Event == "" || strings.TrimSpace(reason) == "" {
		return ErrInvalidNotification
	}
	switch rule.Type {
	case NotifyTypeWebhook, NotifyTypeEmail, NotifyTypeEvent, NotifyTypeWeCom, NotifyTypeDingTalk, NotifyTypeFeishu, NotifyTypeSlack:
	default:
		return ErrInvalidNotification
	}
	// Webhook destinations come from the configured sender; email needs an explicit recipient.
	if rule.Type == NotifyTypeEmail && rule.Recipient == "" {
		return ErrInvalidNotification
	}
	facts := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: rule.ID}
	if rule.Revision == 0 {
		facts.ResourceID = 0
	}
	if err := authorization.Require(ctx, "notify.notification.rules.update", facts); err != nil {
		return err
	}
	r, err := uc.managementRepo()
	if err != nil {
		return err
	}
	return r.SaveRule(authorization.WithWriteReason(ctx, reason), rule)
}
func (uc *NotifyUsecase) TestNotificationRule(ctx context.Context, id int64, reason string) (*Notification, error) {
	ctx, err := uc.authorizeManagement(ctx, "notify.notification.test")
	if err != nil {
		return nil, err
	}
	if id <= 0 || strings.TrimSpace(reason) == "" {
		return nil, ErrInvalidNotification
	}
	if err := authorization.Require(ctx, "notify.notification.test", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
		return nil, err
	}
	r, err := uc.managementRepo()
	if err != nil {
		return nil, err
	}
	return r.TestRule(authorization.WithWriteReason(ctx, reason), id)
}
func (uc *NotifyUsecase) DispatchEvent(ctx context.Context, event, subject, content, fallbackType, fallbackRecipient string) ([]*Notification, error) {
	if err := uc.authorizeSystem(ctx, "/api.notify.v1.NotifyService/CreateNotification", "notify.notifications", "notify.notification.read"); err != nil {
		return nil, err
	}
	r, err := uc.managementRepo()
	if err != nil {
		return nil, err
	}
	rules, err := r.EventRules(ctx, event)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		n, err := uc.CreateNotification(ctx, fallbackType, fallbackRecipient, subject, content)
		return []*Notification{n}, err
	}
	out := []*Notification{}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		n := &Notification{Type: rule.Type, Recipient: rule.Recipient, Subject: subject, Content: content, Status: NotifyStatusPending, CreatedAt: time.Now()}
		if err := uc.repo.Create(ctx, n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func (uc *NotifyUsecase) authorizeManagement(ctx context.Context, op string) (context.Context, error) {
	if !authorization.External(ctx) {
		return ctx, nil
	}
	return authorization.Prepare(ctx, uc.authorization, "notify.notifications", op)
}
