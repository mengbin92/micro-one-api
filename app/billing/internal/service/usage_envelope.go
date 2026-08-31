package service

import (
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
)

// usageEnvelopeFromProto converts the v1 wire envelope into the billing
// domain view (token-usage-billing-semantics-remediation §5.3/§6.2). Nil in,
// nil out: a v1 request without an envelope is detected by the usecase as a
// producer contract error via UsageContractVersion.
func usageEnvelopeFromProto(env *billingv1.UsageEnvelope) *biz.UsageEnvelopeData {
	if env == nil {
		return nil
	}
	out := &biz.UsageEnvelopeData{
		ParseStatus:    env.ParseStatus,
		Semantics:      env.Semantics,
		DecisionReason: env.DecisionReason,
		Canonical:      canonicalBucketsFromProto(env.Canonical),
	}
	if env.Reported != nil {
		out.Protocol = env.Reported.SourceProtocol
		out.FieldShape = env.Reported.FieldShape
		out.ReportedPromptTokens = env.Reported.PromptTokens
		out.ReportedOutputTokens = env.Reported.OutputTokens
		out.ReportedCacheReadTokens = env.Reported.CacheReadTokens
		out.ReportedCacheCreation5mTokens = env.Reported.CacheCreation_5MTokens
		out.ReportedCacheCreation1hTokens = env.Reported.CacheCreation_1HTokens
		out.ReportedTotalTokens = env.Reported.TotalTokens
	}
	out.SubsetCandidate = canonicalBucketsFromProto(env.SubsetCandidate)
	out.ExclusiveCandidate = canonicalBucketsFromProto(env.ExclusiveCandidate)
	return out
}

func canonicalBucketsFromProto(u *billingv1.CanonicalUsageV1) *biz.CanonicalBuckets {
	if u == nil {
		return nil
	}
	return &biz.CanonicalBuckets{
		UncachedInputTokens:   u.UncachedInputTokens,
		CacheReadTokens:       u.CacheReadTokens,
		CacheCreation5mTokens: u.CacheCreation_5MTokens,
		CacheCreation1hTokens: u.CacheCreation_1HTokens,
		OutputTokens:          u.OutputTokens,
	}
}
