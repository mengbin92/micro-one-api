package server

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	logv1 "micro-one-api/api/log/v1"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

type usageLogInput struct {
	UserID                 int64
	TokenID                int64
	TokenName              string
	RequestID              string
	Endpoint               string
	ModelName              string
	Quota                  int64
	PromptTokens           int64
	CompletionTokens       int64
	CacheReadTokens        int64
	CacheCreation5mTokens  int64
	CacheCreation1hTokens  int64
	ChannelID              int64
	SubscriptionAccountID  int64
	Group                  string
	SessionHash            string
	SessionWindowLimitUSD  float64
	ElapsedTime            int64
	IsStream               bool
}

func (s *HTTPServer) ingestUsageLog(ctx context.Context, in usageLogInput) {
	if s.logClient == nil {
		metrics.UsageLogIngestTotal.WithLabelValues("skipped").Inc()
		return
	}
	// Phase 1 (PR 2) carries cache_creation buckets in the human-readable
	// Message because api/log/v1 IngestLogRequest does not yet have dedicated
	// cache_creation fields. PR 3 extends log.proto + billing.proto, runs
	// `make api`, and wires CacheCreation5mTokens / CacheCreation1hTokens as
	// top-level proto fields here. Do not duplicate-count: the Message is the
	// only carrier until PR 3, and structured logging (logUpstreamUsage) is
	// the operations view.
	message := applogger.Sanitize(fmt.Sprintf("model=%s quota=%d prompt_tokens=%d completion_tokens=%d cache_read_tokens=%d cache_creation_5m_tokens=%d cache_creation_1h_tokens=%d channel=%d", in.ModelName, in.Quota, in.PromptTokens, in.CompletionTokens, in.CacheReadTokens, in.CacheCreation5mTokens, in.CacheCreation1hTokens, in.ChannelID))
	_, err := s.logClient.IngestLog(ctx, &logv1.IngestLogRequest{
		Level:                 "consume",
		Message:               message,
		Source:                "relay-gateway",
		RequestId:             in.RequestID,
		UserId:                in.UserID,
		TokenName:             usageTokenName(in),
		ModelName:             in.ModelName,
		Quota:                 in.Quota,
		PromptTokens:          in.PromptTokens,
		CompletionTokens:      in.CompletionTokens,
		CacheReadTokens:       in.CacheReadTokens,
		ChannelId:             in.ChannelID,
		SubscriptionAccountId: in.SubscriptionAccountID,
		ElapsedTime:           in.ElapsedTime,
		IsStream:              in.IsStream,
	})
	if err != nil && applogger.Log != nil {
		metrics.UsageLogIngestTotal.WithLabelValues("error").Inc()
		applogger.Log.Warn("failed to ingest usage log", zap.Error(err))
		return
	}
	metrics.UsageLogIngestTotal.WithLabelValues("success").Inc()
}

func logUpstreamUsage(in usageLogInput) {
	// cache_read ratio denominator is the cache-normalized input total
	// (uncached_input + cache_read + cache_creation), per ADR §2 and
	// cc-switch's cacheHitRate definition. For OpenAI-protocol requests the
	// caller still passes prompt_tokens inclusive of cached; the ratio is an
	// operational signal only, billing uses the canonical buckets.
	cacheCreationTotal := in.CacheCreation5mTokens + in.CacheCreation1hTokens
	cacheRatio := float64(0)
	cacheDenominator := in.PromptTokens + cacheCreationTotal
	if cacheDenominator > 0 {
		cacheRatio = float64(in.CacheReadTokens) / float64(cacheDenominator)
	}
	nonCachedInputTokens := in.PromptTokens
	if in.CacheReadTokens > 0 {
		nonCachedInputTokens = in.PromptTokens - in.CacheReadTokens
		if nonCachedInputTokens < 0 {
			nonCachedInputTokens = 0
		}
	}
	applogger.Log.Info("upstream usage reported",
		zap.String("request_id", in.RequestID),
		zap.String("endpoint", in.Endpoint),
		zap.String("model", in.ModelName),
		zap.Int64("user_id", in.UserID),
		zap.Int64("channel_id", in.ChannelID),
		zap.Bool("is_stream", in.IsStream),
		zap.Int64("total_tokens", in.Quota),
		zap.Int64("upstream_input_tokens", in.PromptTokens),
		zap.Int64("input_tokens", nonCachedInputTokens),
		zap.Int64("output_tokens", in.CompletionTokens),
		zap.Int64("cache_read_tokens", in.CacheReadTokens),
		zap.Int64("cache_creation_5m_tokens", in.CacheCreation5mTokens),
		zap.Int64("cache_creation_1h_tokens", in.CacheCreation1hTokens),
		zap.Int64("cache_creation_tokens", cacheCreationTotal),
		zap.Float64("cache_read_input_ratio", cacheRatio),
	)
}
