package biz

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
)

type ChannelHealthChecker struct {
	client          ChannelProbeClient
	providerFactory *relayprovider.ProviderFactory
	cfg             ChannelHealthCheckerConfig
}

func NewChannelHealthChecker(client ChannelProbeClient, cfg ChannelHealthCheckerConfig) *ChannelHealthChecker {
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
	startedAt := time.Now()
	status := "success"
	defer func() {
		metrics.ChannelHealthCheckRunsTotal.WithLabelValues(status).Inc()
		metrics.ChannelHealthCheckRunDuration.WithLabelValues(status).Observe(time.Since(startedAt).Seconds())
	}()
	page := int32(1)
	for {
		channels, err := c.client.ListEnabledChannels(ctx, page, c.cfg.PageSize)
		if err != nil {
			status = "error"
			return
		}
		for _, channel := range channels {
			if !supportsModelsProbe(channel.Type) {
				continue
			}
			c.probeChannel(ctx, channel.ID)
		}
		if len(channels) < int(c.cfg.PageSize) {
			return
		}
		page++
	}
}

func (c *ChannelHealthChecker) probeChannel(ctx context.Context, channelID int64) {
	startedAt := time.Now()
	detail, err := c.client.GetChannelDetail(ctx, channelID)
	if err != nil || detail == nil {
		observeHealthProbe("error", "channel_detail", time.Since(startedAt))
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	provider, err := c.providerFactory.CreateProviderWithConfig(detail.Type, detail.BaseURL, detail.Key, relayprovider.ProviderConfig{
		APIVersion: detail.APIVersion,
	})
	if err != nil {
		observeHealthProbe("error", healthProbeReason(err), time.Since(startedAt))
		c.record(context.WithoutCancel(ctx), channelID, false, err.Error(), time.Since(startedAt).Milliseconds())
		return
	}
	resp, err := provider.Forward(probeCtx, &relayprovider.RawRequest{
		Method: http.MethodGet,
		Path:   "/models",
		Header: http.Header{"Accept": []string{"application/json"}},
	})
	if err == nil {
		err = validateModelsProbeResponse(resp)
	}
	responseTime := time.Since(startedAt).Milliseconds()
	if err != nil {
		observeHealthProbe("error", healthProbeReason(err), time.Since(startedAt))
		c.record(context.WithoutCancel(ctx), channelID, false, err.Error(), responseTime)
		return
	}
	observeHealthProbe("success", "none", time.Since(startedAt))
	c.record(context.WithoutCancel(ctx), channelID, true, "", responseTime)
}

func validateModelsProbeResponse(resp *relayprovider.RawResponse) error {
	if resp == nil {
		return fmt.Errorf("invalid models response: empty response")
	}
	var payload struct {
		Data   jsonx.RawMessage `json:"data"`
		Models jsonx.RawMessage `json:"models"`
	}
	if err := jsonx.Unmarshal(resp.Body, &payload); err != nil {
		return fmt.Errorf("invalid models response: %w", err)
	}
	raw := payload.Data
	if len(raw) == 0 {
		raw = payload.Models
	}
	if len(raw) == 0 {
		return fmt.Errorf("invalid models response: missing data or models list")
	}
	var models []jsonx.RawMessage
	if err := jsonx.Unmarshal(raw, &models); err != nil {
		return fmt.Errorf("invalid models response: model list: %w", err)
	}
	return nil
}

func (c *ChannelHealthChecker) record(ctx context.Context, channelID int64, success bool, message string, responseTime int64) {
	if c == nil || c.client == nil || channelID <= 0 {
		return
	}
	_ = c.client.RecordChannelHealth(ctx, channelID, success, message, responseTime)
}

func observeHealthProbe(status, reason string, duration time.Duration) {
	metrics.ChannelHealthProbeTotal.WithLabelValues(status, reason).Inc()
	metrics.ChannelHealthProbeDuration.WithLabelValues(status).Observe(duration.Seconds())
}

func healthProbeReason(err error) string {
	if err == nil {
		return "none"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "invalid models response"):
		return "invalid_response"
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "unsupported"):
		return "unsupported_provider"
	case strings.Contains(msg, "ssrf"), strings.Contains(msg, "private ip"), strings.Contains(msg, "localhost"):
		return "ssrf_blocked"
	case strings.Contains(msg, "status"):
		return "upstream_status"
	default:
		return "upstream_error"
	}
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
