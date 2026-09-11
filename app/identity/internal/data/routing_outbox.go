package data

import (
	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/routingoutbox"
)

func (r *Repository) StartRoutingOutbox() func() {
	return routingoutbox.Start(r.db, r.redis, "identity", func(err error) {
		applogger.Log.Warn("routing outbox delivery pending; will retry", zap.Error(err))
	})
}
