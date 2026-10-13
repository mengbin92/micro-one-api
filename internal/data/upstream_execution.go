package data

import (
	"context"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/platform/upstreamclient"
)

func (a *ChannelAdapter) ClaimUpstreamExecution(ctx context.Context, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	return upstreamclient.ClaimExecution(ctx, a.client, claim)
}

func (c *channelClient) ClaimUpstreamExecution(ctx context.Context, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	return upstreamclient.ClaimExecution(ctx, c.client, claim)
}
