package events

const (
	TopicChannelChanged = "channel.changed"
	TopicQuotaReserved  = "billing.quota.reserved"
	TopicQuotaCommitted = "billing.quota.committed"
	TopicQuotaReleased  = "billing.quota.released"
	TopicRelayFailed    = "relay.request.failed"
	TopicRelayFinished  = "relay.request.finished"
)
