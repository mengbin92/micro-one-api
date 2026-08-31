package server

import (
	"context"
	"fmt"
	"math"
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
	//
	// Deprecated (token-usage-billing-semantics-remediation §5.3): kept only
	// for the legacy wire dual-write while old billing consumers exist. The
	// parser verdict in Usage is authoritative for the v1 contract.
	PromptExclusive bool

	// Usage is the parser verdict envelope of the FINAL attempt (§4.1). Nil
	// only on legacy handler paths that have not been migrated to envelope
	// parsing; those keep the flat legacy fields above.
	Usage *relaybiz.UsageEnvelope
}

// applyEnvelope fills the legacy dual-write fields from the envelope's
// reported usage (§5.3: legacy fields keep their OLD meanings — reported
// prompt, not uncached — so old billing consumers are unaffected) and stores
// the envelope for the v1 producer path.
func (in *usageLogInput) applyEnvelope(env relaybiz.UsageEnvelope) {
	in.Usage = &env
	reported := env.Reported
	in.PromptTokens = reported.PromptTokens
	in.CompletionTokens = reported.OutputTokens
	in.CacheReadTokens = reported.CacheReadTokens
	in.CacheCreation5mTokens = reported.CacheCreation5mTokens
	in.CacheCreation1hTokens = reported.CacheCreation1hTokens
	in.Quota = reported.TotalTokens
	if in.Quota <= 0 {
		in.Quota = env.BillableTotal()
	}
	if env.ParseStatus == relaybiz.UsageParseEstimated {
		// Estimator path: no reported values exist; mirror the
		// estimator-provable buckets into the legacy fields so old consumers
		// see the same charge as before the migration.
		canonical := env.CanonicalOrZero()
		if in.PromptTokens == 0 {
			in.PromptTokens = canonical.UncachedInputTokens
		}
		if in.CompletionTokens == 0 {
			in.CompletionTokens = canonical.OutputTokens
		}
		if in.Quota <= 0 {
			in.Quota = canonical.BillableTotal()
		}
	}
}

// billableTokens is the token count used as CommitQuota actual_tokens: the
// canonical billable total when an envelope exists, else the legacy quota.
func (in usageLogInput) billableTokens() int64 {
	if in.Usage != nil {
		if total, ok := safeRelayCanonicalTotal(in.Usage.Canonical); ok && total > 0 {
			return total
		}
		// Ambiguous settlement chooses the cheaper interpretation. With
		// non-negative prices the subset candidate is never more expensive
		// than the exclusive candidate, and it also has the lower token total.
		if in.Usage.ParseStatus == relaybiz.UsageParseAmbiguous {
			if total, ok := safeRelayCanonicalTotal(in.Usage.SubsetCandidate); ok && total > 0 {
				return total
			}
		}
	}
	return in.Quota
}

func safeRelayCanonicalTotal(u *relaybiz.CanonicalUsage) (int64, bool) {
	if u == nil {
		return 0, false
	}
	var total int64
	for _, value := range []int64{u.UncachedInputTokens, u.CacheReadTokens, u.CacheCreation5mTokens, u.CacheCreation1hTokens, u.OutputTokens} {
		if value < 0 || value > math.MaxInt64-total {
			return 0, false
		}
		total += value
	}
	return total, true
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
	// §6.2/§9.1 display contract: dual-write the canonical fields once the
	// producer gate is on. quota keeps its legacy reported-total meaning.
	if in.Usage != nil && canonicalUsageProducerEnabled() {
		env := in.Usage
		req.UncachedInputTokens = env.CanonicalOrZero().UncachedInputTokens
		req.ReportedPromptTokens = env.Reported.PromptTokens
		req.ReportedTotalTokens = env.Reported.TotalTokens
		req.BillableTotalTokens = in.billableTokens()
		req.UsageSemantics = string(env.Semantics)
		req.UsageProtocol = env.Reported.SourceProtocol
		req.UsageFieldShape = env.Reported.FieldShape
		req.UsageParseStatus = string(env.ParseStatus)
		req.UsageContractVersion = env.ContractVersion
		req.CanonicalPresent = env.Canonical != nil
		req.UsageDecisionReason = env.DecisionReason
	}
	var err error
	for attempt := range 3 {
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
	// The parser's canonical uncached-input bucket is authoritative; the
	// PromptExclusive-based recomputation is the legacy fallback for paths
	// without an envelope (§4.1: billing/display never re-derive semantics).
	nonCachedInputTokens := in.PromptTokens
	if in.Usage != nil && in.Usage.Canonical != nil {
		nonCachedInputTokens = in.Usage.Canonical.UncachedInputTokens
	} else if in.CacheReadTokens > 0 && !in.PromptExclusive {
		nonCachedInputTokens = max(in.PromptTokens-in.CacheReadTokens, 0)
	}
	cacheDenominator := nonCachedInputTokens + in.CacheReadTokens + cacheCreationTotal
	cacheRatio := float64(0)
	if cacheDenominator > 0 {
		cacheRatio = float64(in.CacheReadTokens) / float64(cacheDenominator)
	}
	fields := []zap.Field{
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
	}
	if in.Usage != nil {
		fields = append(fields,
			zap.Int64("reported_total_tokens", in.Usage.Reported.TotalTokens),
			zap.Int64("billable_total_tokens", in.billableTokens()),
			zap.String("usage_semantics", string(in.Usage.Semantics)),
			zap.String("usage_protocol", in.Usage.Reported.SourceProtocol),
			zap.String("usage_parse_status", string(in.Usage.ParseStatus)),
			zap.String("usage_decision_reason", in.Usage.DecisionReason),
		)
		recordUsageSemanticsMetrics(in)
	}
	applogger.Log.Info("upstream usage reported", fields...)
}

// recordUsageSemanticsMetrics emits the §9 semantics/invariant counters at
// the relay boundary — the single point where both the parser verdict and
// the execution source are known. Labels stay low-cardinality; request and
// model identifiers stay in the structured log above.
func recordUsageSemanticsMetrics(in usageLogInput) {
	env := in.Usage
	protocol := env.Reported.SourceProtocol
	if protocol == "" {
		protocol = "unknown"
	}
	sourceKind := in.SourceKind
	if sourceKind == "" {
		sourceKind = "unknown"
	}
	semantics := string(env.Semantics)
	if semantics == "" {
		semantics = "none"
	}
	if metrics.TokenUsageSemanticsTotal != nil {
		metrics.TokenUsageSemanticsTotal.WithLabelValues(protocol, semantics, sourceKind).Inc()
	}
	recordInvariantMismatch := func(reason string) {
		if metrics.TokenUsageInvariantMismatchTotal != nil {
			metrics.TokenUsageInvariantMismatchTotal.WithLabelValues(reason, protocol, sourceKind).Inc()
		}
	}
	if env.ParseStatus == relaybiz.UsageParseAmbiguous && env.DecisionReason != "" {
		recordInvariantMismatch(env.DecisionReason)
	}
	// reported_total_mismatch (§2.5 anomaly signal only — never a billing
	// input): the upstream-reported total must equal one of the totals the
	// proven semantics implies (prompt+output, or the five-bucket sum).
	if rt := env.Reported.TotalTokens; rt > 0 {
		reported := env.Reported
		legacyTotal := reported.PromptTokens + reported.OutputTokens
		bucketTotal := legacyTotal + reported.CacheReadTokens + reported.CacheCreation5mTokens + reported.CacheCreation1hTokens
		if rt != legacyTotal && rt != bucketTotal {
			recordInvariantMismatch(relaybiz.UsageReasonReportedTotalMismatch)
		}
	}
}
