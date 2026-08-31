package server

import (
	"context"
	"math"
	"testing"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

// F23 / §5.3 dual-write contract: regardless of the producer gate, the
// legacy fields keep their OLD meanings (reported prompt, NOT uncached) so
// old billing consumers never double-subtract; the v1 envelope is attached
// only when the gate is on.
func TestCommitQuotaWithResponse_DualWriteContract(t *testing.T) {
	env := relaybiz.UsageEnvelope{
		ContractVersion: relaybiz.UsageContractVersionV1,
		ParseStatus:     relaybiz.UsageParseVerified,
		Semantics:       relaybiz.UsageSemanticsAnthropicExclusive,
		Reported: relaybiz.ReportedUsage{
			PromptTokens: 130, OutputTokens: 9, CacheReadTokens: 45056, TotalTokens: 139,
			SourceProtocol: "anthropic_messages", FieldShape: "input_tokens+cache_read_input_tokens",
		},
		Canonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9},
	}
	detail := usageLogInput{
		UserID:     1,
		TokenName:  "tok",
		Endpoint:   "/v1/chat/completions",
		ModelName:  "glm-5.3",
		SourceKind: relaybiz.UpstreamSourceSubscription,
		// Legacy dual-write fields with their OLD meanings (reported, not
		// uncached): prompt_exclusive=true says "don't subtract".
		PromptTokens:          130,
		CompletionTokens:      9,
		CacheReadTokens:       45056,
		PromptExclusive:       true,
		SubscriptionAccountID: 7,
		UpstreamModelID:       "glm-5.3",
		Usage:                 &env,
	}

	t.Run("gate off: legacy fields only, no v1 contract", func(t *testing.T) {
		t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "")
		billing := &rawBillingClient{}
		s := &HTTPServer{billingClient: billing}
		if err := s.commitQuota(context.Background(), "res-1", 45195, true, detail); err != nil {
			t.Fatalf("commitQuota: %v", err)
		}
		req := billing.commitRequests[0]
		if req.UsageContractVersion != 0 || req.Usage != nil {
			t.Fatalf("gate off must not send v1 contract: version=%d usage=%v", req.UsageContractVersion, req.Usage)
		}
		// Legacy fields keep old meanings: prompt is the REPORTED 130, not
		// an uncached rewrite, so old billing with prompt_exclusive=true
		// charges exactly as before.
		if req.PromptTokens != 130 || req.CacheReadTokens != 45056 || !req.PromptExclusive {
			t.Fatalf("legacy fields changed meaning: prompt=%d cache=%d exclusive=%v",
				req.PromptTokens, req.CacheReadTokens, req.PromptExclusive)
		}
	})

	t.Run("gate on: v1 envelope attached AND legacy fields unchanged", func(t *testing.T) {
		t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "1")
		billing := &rawBillingClient{}
		s := &HTTPServer{billingClient: billing}
		if err := s.commitQuota(context.Background(), "res-1", 45195, true, detail); err != nil {
			t.Fatalf("commitQuota: %v", err)
		}
		req := billing.commitRequests[0]
		if req.UsageContractVersion != 1 || req.Usage == nil {
			t.Fatalf("gate on must send v1 envelope: version=%d usage=%v", req.UsageContractVersion, req.Usage)
		}
		if req.PromptTokens != 130 || req.CacheReadTokens != 45056 || !req.PromptExclusive {
			t.Fatal("dual-write contract violated: legacy fields changed when envelope added")
		}
		got := req.Usage
		if got.ParseStatus != "verified" || got.Semantics != "anthropic_exclusive" {
			t.Fatalf("envelope verdict = %q/%q", got.ParseStatus, got.Semantics)
		}
		if got.Canonical == nil || got.Canonical.UncachedInputTokens != 130 || got.Canonical.CacheReadTokens != 45056 {
			t.Fatalf("envelope canonical = %+v", got.Canonical)
		}
		if got.Reported == nil || got.Reported.TotalTokens != 139 || got.Reported.SourceProtocol != "anthropic_messages" {
			t.Fatalf("envelope reported = %+v", got.Reported)
		}
	})
}

func TestEnvelopeFromProviderUsage_InvalidCanonicalIsAmbiguous(t *testing.T) {
	for _, canonical := range []*relayprovider.CanonicalUsage{
		{UncachedInputTokens: -1, ReportedPromptTokens: -1, Protocol: "anthropic_messages"},
		{UncachedInputTokens: math.MaxInt64, OutputTokens: 1, ReportedPromptTokens: math.MaxInt64, Protocol: "anthropic_messages"},
		{UncachedInputTokens: 1, CacheReadTokens: 2, ReportedPromptTokens: 1, Protocol: "anthropic_messages"},
	} {
		env := envelopeFromProviderUsage(relayprovider.Usage{}, canonical)
		if env.ParseStatus != relaybiz.UsageParseAmbiguous || env.Canonical != nil {
			t.Fatalf("invalid provider canonical was trusted: %+v", env)
		}
	}
}

func TestCommitQuota_UsesCanonicalOrConservativeTotal(t *testing.T) {
	t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "1")
	cases := []struct {
		name string
		env  relaybiz.UsageEnvelope
		want int64
	}{
		{
			name: "verified subset canonical",
			env: relaybiz.UsageEnvelope{
				ParseStatus: relaybiz.UsageParseVerified,
				Canonical:   &relaybiz.CanonicalUsage{UncachedInputTokens: 60, CacheReadTokens: 40, OutputTokens: 10},
			},
			want: 110,
		},
		{
			name: "ambiguous conservative candidate",
			env: relaybiz.UsageEnvelope{
				ParseStatus:     relaybiz.UsageParseAmbiguous,
				SubsetCandidate: &relaybiz.CanonicalUsage{CacheReadTokens: 45056, OutputTokens: 9},
				ExclusiveCandidate: &relaybiz.CanonicalUsage{
					UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9,
				},
			},
			want: 45065,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			billing := &rawBillingClient{}
			s := &HTTPServer{billingClient: billing}
			detail := usageLogInput{Quota: 139, Usage: &tc.env}
			if err := s.commitQuota(context.Background(), "res", 139, true, detail); err != nil {
				t.Fatal(err)
			}
			if got := billing.commitRequests[0].ActualTokens; got != tc.want {
				t.Fatalf("actual_tokens=%d want=%d", got, tc.want)
			}
		})
	}
}
