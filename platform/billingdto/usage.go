package billingdto

import (
	billingv1 "micro-one-api/api/billing/v1"
	billingdomain "micro-one-api/domain/billing"
)

// EnvelopeFromProto converts the v1 wire envelope into the billing
// domain view (token-usage-billing-semantics-remediation §5.3/§6.2). Nil in,
// nil out: a v1 request without an envelope is detected by the usecase as a
// producer contract error via UsageContractVersion.
func EnvelopeFromProto(env *billingv1.UsageEnvelope) *billingdomain.UsageEnvelopeData {
	if env == nil {
		return nil
	}
	out := &billingdomain.UsageEnvelopeData{
		ParserRevision: env.ParserRevision, ContractRevision: env.ContractRevision, PresentPaths: append([]string(nil), env.PresentPaths...),
		ParseStatus:    env.ParseStatus,
		Semantics:      env.Semantics,
		DecisionReason: env.DecisionReason,
		Canonical:      bucketsFromProto(env.Canonical),
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
	out.SubsetCandidate = bucketsFromProto(env.SubsetCandidate)
	out.ExclusiveCandidate = bucketsFromProto(env.ExclusiveCandidate)
	return out
}

func bucketsFromProto(u *billingv1.CanonicalUsageV1) *billingdomain.CanonicalBuckets {
	if u == nil {
		return nil
	}
	return &billingdomain.CanonicalBuckets{
		UncachedInputTokens:   u.UncachedInputTokens,
		CacheReadTokens:       u.CacheReadTokens,
		CacheCreation5mTokens: u.CacheCreation_5MTokens,
		CacheCreation1hTokens: u.CacheCreation_1HTokens,
		OutputTokens:          u.OutputTokens,
	}
}

func CommitUsage(req *billingv1.CommitQuotaRequest) billingdomain.LedgerUsage {
	cost := req.UpstreamCost
	if cost <= 0 {
		cost = req.ActualCost
	}
	if cost < 0 {
		cost = 0
	}
	return billingdomain.LedgerUsage{TokenName: req.TokenName, Endpoint: req.Endpoint, PromptTokens: req.PromptTokens, CompletionTokens: req.CompletionTokens, CacheReadTokens: req.CacheReadTokens, CacheCreation5mTokens: req.CacheCreation_5MTokens, CacheCreation1hTokens: req.CacheCreation_1HTokens, UpstreamCost: cost, ElapsedTime: req.ElapsedTime, IsStream: req.IsStream, SubscriptionAccountID: req.SubscriptionAccountId, UpstreamModelID: req.UpstreamModelId, SourceKind: req.SourceKind, PromptExclusive: req.PromptExclusive, UsageContractVersion: req.UsageContractVersion, Envelope: EnvelopeFromProto(req.Usage)}
}
