package server

import (
	"context"

	"micro-one-api/domain/requesttrace"
	relaybiz "micro-one-api/internal/biz"
)

func channelAttemptContext(ctx context.Context, root string, number int32, channel *relaybiz.Channel, model string) context.Context {
	source := relaybiz.UpstreamSourceChannel
	if channel != nil && channel.SubscriptionAccountID > 0 {
		source = relaybiz.UpstreamSourceSubscription
	}
	return requesttrace.WithAttempt(ctx, requesttrace.Attempt{
		RootRequestID: root, Number: number, SourceKind: source, UpstreamModelID: model,
	})
}
