package main

import (
	"context"
	"fmt"
	"time"

	notifyv1 "micro-one-api/api/notify/v1"
	billingbiz "micro-one-api/app/billing/internal/biz"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// grpcNotifier adapts the notify-worker gRPC client to the billing
// biz.Notifier interface. It lives next to wire so the internal billing
// package stays free of transport concerns.
type grpcNotifier struct {
	client     notifyv1.NotifyServiceClient
	notifyType string
}

type expiryNotifier struct{ notifier billingbiz.Notifier }

func (n expiryNotifier) NotifyExpiry(ctx context.Context, event subscriptionbiz.ExpiryNotification) error {
	if n.notifier == nil {
		return fmt.Errorf("expiry notifier disabled")
	}
	return n.notifier.CreateNotification(ctx, "event", "",
		fmt.Sprintf("[subscription] expiry reminder #%d", event.SubscriptionID),
		fmt.Sprintf("subscription %d for user %d expires at %s", event.SubscriptionID, event.UserID, time.Unix(event.ExpiresAt, 0).UTC().Format(time.RFC3339)))
}

func newGRPCNotifier(client notifyv1.NotifyServiceClient, notifyType string) *grpcNotifier {
	if notifyType == "" {
		notifyType = "event"
	}
	return &grpcNotifier{client: client, notifyType: notifyType}
}

func (n *grpcNotifier) CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) error {
	if n.client == nil {
		return fmt.Errorf("notify client not configured")
	}
	nt := notifyType
	if nt == "" {
		nt = n.notifyType
	}
	_, err := n.client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
		Type:      nt,
		Recipient: recipient,
		Subject:   subject,
		Content:   content,
	})
	return err
}
