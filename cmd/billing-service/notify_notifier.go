package main

import (
	"context"
	"fmt"

	notifyv1 "micro-one-api/api/notify/v1"
)

// grpcNotifier adapts the notify-worker gRPC client to the billing
// biz.Notifier interface. It lives next to wire so the internal billing
// package stays free of transport concerns.
type grpcNotifier struct {
	client    notifyv1.NotifyServiceClient
	notifyType string
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
