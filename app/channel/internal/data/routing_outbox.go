package data

import "micro-one-api/platform/routingoutbox"

func (r *Repository) StartRoutingOutbox() func() {
	return routingoutbox.Start(r.db, r.redis, "channel", routingoutbox.Report)
}
