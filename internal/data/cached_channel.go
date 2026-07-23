package data

import (
	"context"

	"google.golang.org/grpc"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	appcache "micro-one-api/platform/cache"
	relaybiz "micro-one-api/internal/biz"
)

// CachedChannelClient wraps ChannelServiceClient.SelectChannel with the shared
// multi-level ChannelCache while delegating all other channel RPCs to the
// underlying client. It implements biz.ChannelClient / ChannelSelector so the
// relay-gateway request path (Plan + RetryExecutor) transparently benefits
// from channel-selection caching without changing call sites.
//
// The cache is only consulted when ExcludeFirstPriority is false — the
// retry/failover path excludes the top-priority tier and must bypass the
// cache to avoid returning the same (failed) channel. Negative results are
// cached lightly: a miss returns the original error to the caller, but
// because SelectChannel returns a single ChannelInfo and there is no nil
// sentinel cached by the loader, only successful selections are cached.
type CachedChannelClient struct {
	channelv1.ChannelServiceClient
	cache *appcache.ChannelCache
}

// NewCachedChannelClient wraps client with ChannelCache. If cache is nil the
// client is returned unchanged so the feature flag off path is a no-op.
func NewCachedChannelClient(client channelv1.ChannelServiceClient, cache *appcache.ChannelCache) channelv1.ChannelServiceClient {
	if cache == nil {
		return client
	}
	return &CachedChannelClient{ChannelServiceClient: client, cache: cache}
}

// SelectChannel consults the channel cache before calling the upstream
// channel service. Failover requests (ExcludeFirstPriority=true) bypass the
// cache so retries do not replay the failed top-priority channel.
func (c *CachedChannelClient) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest, opts ...grpc.CallOption) (*channelv1.SelectChannelReply, error) {
	if c.cache == nil || req.GetExcludeFirstPriority() {
		return c.ChannelServiceClient.SelectChannel(ctx, req, opts...)
	}

	group := req.GetGroup()
	model := req.GetModel()
	channels, err := c.cache.Get(ctx, group, model)
	if err == nil && len(channels) > 0 {
		// Cache hit — return the first (highest-priority) candidate.
		return &channelv1.SelectChannelReply{Channel: channels[0]}, nil
	}

	// Cache miss (or cache error) — call upstream.
	reply, err := c.ChannelServiceClient.SelectChannel(ctx, req, opts...)
	if err != nil || reply == nil || reply.GetChannel() == nil {
		return reply, err
	}

	// Populate the cache on success. A Set failure is non-fatal: the selection
	// still succeeds, the next request simply misses again.
	_ = c.cache.Set(ctx, group, model, []*commonv1.ChannelInfo{reply.GetChannel()})
	return reply, nil
}

// channelInfoToBizChannel converts a commonv1.ChannelInfo to the biz Channel.
// It mirrors ChannelAdapter.SelectChannel's mapping so cached selections can
// be reused by the biz ChannelClient path.
func channelInfoToBizChannel(ch *commonv1.ChannelInfo) *relaybiz.Channel {
	if ch == nil {
		return nil
	}
	c := &relaybiz.Channel{
		ID:       ch.Id,
		Type:     ch.Type,
		Name:     ch.Name,
		Status:   ch.Status,
		BaseURL:  ch.BaseUrl,
		Group:    ch.Group,
		Models:   splitModels(ch.Models),
		Priority: ch.Priority,
		Key:      ch.Key,
		ModelMapping: ch.GetModelMapping(),
	}
	if ch.Config != nil {
		c.Config.APIVersion = ch.Config.ApiVersion
	}
	return c
}
