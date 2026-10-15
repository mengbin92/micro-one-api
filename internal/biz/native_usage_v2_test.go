package biz

import (
	"testing"

	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/upstream/evidence"
)

func TestNativeUsageV2PreservesAmbiguousCacheEvidence(t *testing.T) {
	r := &evidence.Record{Complete: true, HTTPStatus: 200, Observation: evidence.Parse([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":5}}}`), "openai_chat")}
	u := NativeUsageV2(r)
	if u.ParseStatus != UsageParseAmbiguous || u.DecisionReason != "cached_exceeds_reported_prompt" || u.Canonical != nil || u.Semantics != "" || u.Reported.CacheReadTokens != 5 || u.SubsetCandidate == nil || u.ExclusiveCandidate == nil {
		t.Fatalf("ambiguous evidence changed: %+v", u)
	}
	if *u.SubsetCandidate != (CanonicalUsage{CacheReadTokens: 5, OutputTokens: 2}) || *u.ExclusiveCandidate != (CanonicalUsage{UncachedInputTokens: 1, CacheReadTokens: 5, OutputTokens: 2}) {
		t.Fatalf("candidates changed: %+v / %+v", u.SubsetCandidate, u.ExclusiveCandidate)
	}
	ledger := NativeBillingUsage(billingdomain.LedgerUsage{}, r)
	if ledger.Envelope.ParseStatus != "ambiguous" || ledger.Envelope.ReportedCacheReadTokens != 5 || ledger.Envelope.SubsetCandidate == nil || ledger.Envelope.ExclusiveCandidate == nil {
		t.Fatalf("billing lost candidates: %+v", ledger.Envelope)
	}
}

func TestNativeUsageV2PreservesFaultWhenExecutionIsIncomplete(t *testing.T) {
	for _, body := range []string{
		`{"usage":{"prompt_tokens":1,"completion_tokens":2,"unknown_units":3}}`,
		`{"usage":{"prompt_tokens":"bad","completion_tokens":2}}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":5}}}`,
	} {
		o := evidence.Parse([]byte(body), "openai_chat")
		u := NativeUsageV2(&evidence.Record{Complete: false, HTTPStatus: 200, Observation: o})
		if string(u.ParseStatus) != o.Status || u.DecisionReason != o.Reason || u.Canonical != nil {
			t.Fatalf("original fault overwritten by interruption: observation=%+v envelope=%+v", o, u)
		}
	}
}
