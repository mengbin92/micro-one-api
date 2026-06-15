package biz

import (
	"context"
	"net/http"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	relayprovider "micro-one-api/internal/relay/provider"
)

type ChannelHealthCheckerConfig struct {
	Enabled  bool
	Interval time.Duration
	Timeout  time.Duration
	PageSize int32
}

type ChannelHealthChecker struct {
	client          channelv1.ChannelServiceClient
	providerFactory *relayprovider.ProviderFactory
	cfg             ChannelHealthCheckerConfig
}

func NewChannelHealthChecker(client channelv1.ChannelServiceClient, cfg ChannelHealthCheckerConfig) *ChannelHealthChecker {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 100
	}
	return &ChannelHealthChecker{
		client:          client,
		providerFactory: relayprovider.NewProviderFactory(cfg.Timeout),
		cfg:             cfg,
	}
}

func (c *ChannelHealthChecker) Run(ctx context.Context) {
	if c == nil || c.client == nil || !c.cfg.Enabled {
		return
	}
	c.CheckOnce(ctx)
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CheckOnce(ctx)
		}
	}
}

func (c *ChannelHealthChecker) CheckOnce(ctx context.Context) {
	if c == nil || c.client == nil {
		return
	}
	page := int32(1)
	for {
		resp, err := c.client.ListChannels(ctx, &channelv1.ListChannelsRequest{
			Page:     page,
			PageSize: c.cfg.PageSize,
			Status:   1,
		})
		if err != nil || resp == nil {
			return
		}
		channels := resp.GetChannels()
		for _, channel := range channels {
			if channel == nil || !supportsModelsProbe(channel.GetType()) {
				continue
			}
			c.probeChannel(ctx, channel.GetId())
		}
		if len(channels) < int(c.cfg.PageSize) {
			return
		}
		page++
	}
}

func (c *ChannelHealthChecker) probeChannel(ctx context.Context, channelID int64) {
	detail, err := c.client.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil || detail == nil || detail.GetChannel() == nil {
		return
	}
	channel := detail.GetChannel()
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	startedAt := time.Now()
	provider, err := c.providerFactory.CreateProviderWithConfig(channel.GetType(), channel.GetBaseUrl(), channel.GetKey(), relayprovider.ProviderConfig{
		APIVersion: channel.GetConfig().GetApiVersion(),
	})
	if err != nil {
		c.record(context.WithoutCancel(ctx), channelID, false, err.Error(), time.Since(startedAt).Milliseconds())
		return
	}
	_, err = provider.Forward(probeCtx, &relayprovider.RawRequest{
		Method: http.MethodGet,
		Path:   "/models",
		Header: http.Header{"Accept": []string{"application/json"}},
	})
	responseTime := time.Since(startedAt).Milliseconds()
	if err != nil {
		c.record(context.WithoutCancel(ctx), channelID, false, err.Error(), responseTime)
		return
	}
	c.record(context.WithoutCancel(ctx), channelID, true, "", responseTime)
}

func (c *ChannelHealthChecker) record(ctx context.Context, channelID int64, success bool, message string, responseTime int64) {
	if c == nil || c.client == nil || channelID <= 0 {
		return
	}
	_, _ = c.client.RecordChannelHealth(ctx, &channelv1.RecordChannelHealthRequest{
		ChannelId:    channelID,
		Success:      success,
		Error:        message,
		ResponseTime: responseTime,
	})
}

func supportsModelsProbe(channelType int32) bool {
	switch channelType {
	case relayprovider.ChannelTypeOpenAI,
		relayprovider.ChannelTypeDeepSeek,
		relayprovider.ChannelTypeMistral,
		relayprovider.ChannelTypeMoonshot,
		relayprovider.ChannelTypeGroq,
		relayprovider.ChannelTypeCohere,
		relayprovider.ChannelTypeBaichuan,
		relayprovider.ChannelTypeZhipu,
		relayprovider.ChannelTypeTongyi,
		relayprovider.ChannelTypeMinimax,
		relayprovider.ChannelTypeTogether,
		relayprovider.ChannelTypeFireworks,
		relayprovider.ChannelTypePerplexity,
		relayprovider.ChannelTypeNovita,
		relayprovider.ChannelTypeOpenRouter,
		relayprovider.ChannelTypeSiliconFlow,
		relayprovider.ChannelTypeOllama,
		relayprovider.ChannelTypeDoubao:
		return true
	default:
		return false
	}
}
