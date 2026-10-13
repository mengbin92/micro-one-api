package data

import (
	"context"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/platform/upstreamclient"
)

func (a *ChannelAdapter) RecordUpstreamObservation(ctx context.Context, o provider.Observation) error {
	return upstreamclient.ReportObservation(ctx, a.client, o)
}

func (c *channelClient) RecordUpstreamObservation(ctx context.Context, o provider.Observation) error {
	return upstreamclient.ReportObservation(ctx, c.client, o)
}
