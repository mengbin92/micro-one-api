package routingoutbox

import (
	"errors"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
)

// Report logs retryable failures without credentials or payloads.
func Report(err error) {
	fields := []zap.Field{zap.Error(err)}
	var delivery *DeliveryError
	if errors.As(err, &delivery) {
		fields = append(fields, zap.String("owner", delivery.Owner), zap.String("operation", delivery.Operation), zap.String("event_id", delivery.EventID))
	}
	applogger.Log.Warn("routing outbox operation failed; will retry", fields...)
}
