package server

import (
	"context"
	"fmt"
	"time"

	logv1 "micro-one-api/api/log/v1"
	relaybiz "micro-one-api/internal/biz"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"

	"go.uber.org/zap"
)

// applyPlanInputs sets the fields derived from the relay plan that are common
// to every usage/commit path: the v0.11.0 Phase 2 §2.2 stable upstream
// cost-key inputs and the Phase 0/1 ADR §3.3 prompt-exclusivity flag. Callers
// that already construct a usageLogInput should call this right after building
// the struct literal so no code path forgets the plan-derived metadata.
func (in *usageLogInput) applyPlanInputs(plan *relaybiz.RelayPlan) {
	if plan == nil {
		return
	}
	// CR 2026-08-05: upstreamCostKeyInputsFromPlan returns (sourceKind,
	// upstreamModelID). The assignment was previously reversed, so
	// in.SourceKind always ended up empty and subscription-sourced traffic
	// was never skipped by recordChannelUsageFromDetail — every request
	// against a synthetic subscription channel id re-triggered the
	// "channel not found" warning.
	in.SourceKind, in.UpstreamModelID = upstreamCostKeyInputsFromPlan(plan)
	in.PromptExclusive = relaybiz.IsPromptExclusiveChannel(plan)
}

// applyChannelInputs records the source that actually executed a request.
// Retry/failover paths cannot use the original plan because the final channel
// may belong to a different source namespace.
func (in *usageLogInput) applyChannelInputs(channel *relaybiz.Channel) {
	if channel == nil {
		return
	}
	in.UpstreamModelID = channel.UpstreamModelID
	in.PromptExclusive = relaybiz.IsPromptExclusiveChannelType(channel.Type)
	if channel.SubscriptionAccountID > 0 {
		in.SourceKind = relaybiz.UpstreamSourceSubscription
		in.SubscriptionAccountID = channel.SubscriptionAccountID
		return
	}
	in.SourceKind = relaybiz.UpstreamSourceChannel
	in.SubscriptionAccountID = 0
}

type usageLogInput struct {
	UserID                int64
	TokenID               int64
	TokenName             string
	RequestID             string
	Endpoint              string
	ModelName             string
	Quota                 int64
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	ChannelID             int64
	SubscriptionAccountID int64
	Group                 string
	SessionHash           string
	SessionWindowLimitUSD float64
	ElapsedTime           int64
	IsStream              bool

	// v0.11.0 Phase 2 §2.2: stable upstream cost-key inputs. Populated from
	// the relay plan so billing can build channel:<id>:<upstream_model_id> /
	// subscription:<id>:<upstream_model_id> instead of the legacy
	// <channel_id>:<public_model_id>.
	UpstreamModelID string
	SourceKind      string

	// PromptExclusive (v0.11.0 Phase 0/1, ADR §3.3): true when prompt and
	// cache_read are mutually exclusive buckets (Anthropic / GLM). Set from
	// the channel type at the relay boundary.
	PromptExclusive bool
}

func (s *HTTPServer) ingestUsageLog(ctx context.Context, in usageLogInput) {
	if s.logClient == nil {
		metrics.UsageLogIngestTotal.WithLabelValues("skipped").Inc()
		return
	}
	message := applogger.Sanitize(fmt.Sprintf("model=%s quota=%d prompt_tokens=%d completion_tokens=%d cache_read_tokens=%d channel=%d", in.ModelName, in.Quota, in.PromptTokens, in.CompletionTokens, in.CacheReadTokens, in.ChannelID))
	dedupeKey := ""
	if in.RequestID != "" {
		dedupeKey = fmt.Sprintf("consume:%d:%s", in.UserID, in.RequestID)
	}
	req := &logv1.IngestLogRequest{
		Level:                  "consume",
		Message:                message,
		Source:                 "relay-gateway",
		RequestId:              in.RequestID,
		UserId:                 in.UserID,
		TokenName:              usageTokenName(in),
		ModelName:              in.ModelName,
		Quota:                  in.Quota,
		PromptTokens:           in.PromptTokens,
		CompletionTokens:       in.CompletionTokens,
		CacheReadTokens:        in.CacheReadTokens,
		CacheCreation_5MTokens: in.CacheCreation5mTokens,
		CacheCreation_1HTokens: in.CacheCreation1hTokens,
		ChannelId:              in.ChannelID,
		SubscriptionAccountId:  in.SubscriptionAccountID,
		ElapsedTime:            in.ElapsedTime,
		IsStream:               in.IsStream,
		DedupeKey:              dedupeKey,
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = s.logClient.IngestLog(ctx, req)
		if err == nil {
			metrics.UsageLogIngestTotal.WithLabelValues("success").Inc()
			return
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
	}
	if err != nil {
		metrics.UsageLogIngestTotal.WithLabelValues("error").Inc()
		applogger.Log.Warn("failed to ingest usage log after retries", zap.String("dedupe_key", dedupeKey), zap.Error(err))
		return
	}
}

func logUpstreamUsage(in usageLogInput) {
	// cache_read ratio denominator is the cache-normalized input total
	// (uncached_input + cache_read + cache_creation), per ADR §2 and
	// cc-switch's cacheHitRate definition. For OpenAI-protocol requests the
	// caller still passes prompt_tokens inclusive of cached; the ratio is an
	// operational signal only, billing uses the canonical buckets.
	cacheCreationTotal := in.CacheCreation5mTokens + in.CacheCreation1hTokens
	// v0.11.0 review L1: for non-exclusive buckets (OpenAI subset protocol)
	// prompt_tokens already includes cache_read_tokens, so subtract them to get
	// the uncached portion before adding cache_read back into the denominator.
	// For exclusive buckets (Anthropic/GLM) prompt_tokens is already uncached.
	nonCachedInputTokens := in.PromptTokens
	if in.CacheReadTokens > 0 && !in.PromptExclusive {
		nonCachedInputTokens = in.PromptTokens - in.CacheReadTokens
		if nonCachedInputTokens < 0 {
			nonCachedInputTokens = 0
		}
	}
	cacheDenominator := nonCachedInputTokens + in.CacheReadTokens + cacheCreationTotal
	cacheRatio := float64(0)
	if cacheDenominator > 0 {
		cacheRatio = float64(in.CacheReadTokens) / float64(cacheDenominator)
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
