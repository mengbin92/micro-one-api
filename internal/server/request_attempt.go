package server

import (
	"context"

	"micro-one-api/domain/requesttrace"
	relaybiz "micro-one-api/internal/biz"
)

func channelAttemptContext(ctx context.Context, root string, number int32, channel *relaybiz.Channel, model string) context.Context {
	source := relaybiz.UpstreamSourceChannel
	var sourceID int64
	if channel != nil {
		sourceID = channel.ID
	}
	if channel != nil && channel.SubscriptionAccountID > 0 {
		source = relaybiz.UpstreamSourceSubscription
		sourceID = channel.SubscriptionAccountID
	}
	return requesttrace.WithAttempt(ctx, requesttrace.Attempt{
		RootRequestID: root, Number: number, SourceKind: source, SourceID: sourceID, UpstreamModelID: model,
		HealthProbeID: channelHealthProbeID(channel),
	})
}

func channelHealthProbeID(channel *relaybiz.Channel) string {
	if channel == nil {
		return ""
	}
	return channel.HealthProbeID
}
