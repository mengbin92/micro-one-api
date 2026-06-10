package biz

import "context"

// Notifier delivers reconciliation alerts to operators. The billing service
// does not depend on a specific transport: callers (typically cmd/wire) wire a
// gRPC client to the notify-worker into this interface. A nil notifier means
// "alerts disabled" and matches the legacy behaviour of only logging.
type Notifier interface {
	CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) error
}

// noopNotifier silently discards notifications. Used when no notify endpoint
// is configured so the job keeps running without spamming logs.
type noopNotifier struct{}

func (noopNotifier) CreateNotification(context.Context, string, string, string, string) error {
	return nil
}

// NoopNotifier returns a Notifier that drops every message.
func NoopNotifier() Notifier { return noopNotifier{} }
