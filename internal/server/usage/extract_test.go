package usage

import (
	"math"
	"testing"

	relaybiz "micro-one-api/internal/biz"
)

func TestExtractEnvelopeFromJSON(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		fallback      int64
		wantStatus    relaybiz.UsageParseStatus
		wantSemantics relaybiz.UsageSemantics
		wantReported  relaybiz.ReportedUsage
		wantCanonical *relaybiz.CanonicalUsage
	}{
		{
			name:       "openai chat completion",
			body:       `{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			wantStatus: relaybiz.UsageParseVerified,
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 10, OutputTokens: 5, TotalTokens: 15,
				SourceProtocol: "openai_chat", FieldShape: "prompt_tokens",
			},
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 10, OutputTokens: 5},
		},
		{
			name:          "openai with cached tokens in details is verified subset",
			body:          `{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}}`,
			wantStatus:    relaybiz.UsageParseVerified,
			wantSemantics: relaybiz.UsageSemanticsOpenAISubset,
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 10, OutputTokens: 5, CacheReadTokens: 3, TotalTokens: 15,
				SourceProtocol: "openai_chat", FieldShape: "prompt_tokens+details.cached_tokens",
			},
			// uncached = prompt - cached; billing must NOT subtract again (F11).
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 7, CacheReadTokens: 3, OutputTokens: 5},
		},
		{
			name:          "anthropic with nested cache creation is verified exclusive",
			body:          `{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":4,"ephemeral_1h_input_tokens":3}}}`,
			wantStatus:    relaybiz.UsageParseVerified,
			wantSemantics: relaybiz.UsageSemanticsAnthropicExclusive,
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 10, OutputTokens: 5, CacheCreation5mTokens: 4, CacheCreation1hTokens: 3,
				SourceProtocol: "anthropic_messages", FieldShape: "input_tokens+cache_creation",
			},
			// uncached = input_tokens, NO subtraction; billable total includes
			// every cache bucket (F12).
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 10, CacheCreation5mTokens: 4, CacheCreation1hTokens: 3, OutputTokens: 5},
		},
		{
			name:          "anthropic cache_read_input_tokens is verified exclusive",
			body:          `{"usage":{"input_tokens":130,"output_tokens":9,"cache_read_input_tokens":45056}}`,
			wantStatus:    relaybiz.UsageParseVerified,
			wantSemantics: relaybiz.UsageSemanticsAnthropicExclusive,
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 130, OutputTokens: 9, CacheReadTokens: 45056,
				SourceProtocol: "anthropic_messages", FieldShape: "input_tokens+cache_read_input_tokens",
			},
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9},
		},
		{
			name:          "provider flattened cache creation buckets",
			body:          `{"usage":{"prompt_tokens":10,"completion_tokens":5,"cache_creation_5m_tokens":4,"cache_creation_1h_tokens":3}}`,
			wantStatus:    relaybiz.UsageParseVerified,
			wantSemantics: relaybiz.UsageSemanticsOpenAISubset,
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 10, OutputTokens: 5, CacheCreation5mTokens: 4, CacheCreation1hTokens: 3,
				SourceProtocol: "openai_chat", FieldShape: "prompt_tokens",
			},
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 10, CacheCreation5mTokens: 4, CacheCreation1hTokens: 3, OutputTokens: 5},
		},
		{
			name:          "cache exceeding reported prompt is ambiguous, not clamped (F22)",
			body:          `{"usage":{"prompt_tokens":130,"completion_tokens":9,"cache_read_tokens":45056}}`,
			wantStatus:    relaybiz.UsageParseAmbiguous,
			wantSemantics: "",
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 130, OutputTokens: 9, CacheReadTokens: 45056,
				SourceProtocol: "openai_chat", FieldShape: "prompt_tokens+cache_read_tokens",
			},
			// No fabricated canonical.
			wantCanonical: nil,
		},
		{
			name:       "fallback estimate when usage missing (F19)",
			body:       `{"choices":[]}`,
			fallback:   42,
			wantStatus: relaybiz.UsageParseEstimated,
			// The estimator proves uncached input only; cache is never
			// fabricated.
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 42},
		},
		{
			name: "total disguised as subset cannot override anthropic markers (F17)",
			// total_tokens == input+output LOOKS like the OpenAI subset
			// relationship, but the Anthropic field names already proved
			// exclusive semantics (§2.5: total never decides semantics).
			body:          `{"usage":{"input_tokens":130,"output_tokens":9,"total_tokens":139,"cache_read_input_tokens":45056}}`,
			wantStatus:    relaybiz.UsageParseVerified,
			wantSemantics: relaybiz.UsageSemanticsAnthropicExclusive,
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 130, OutputTokens: 9, CacheReadTokens: 45056, TotalTokens: 139,
				SourceProtocol: "anthropic_messages", FieldShape: "input_tokens+cache_read_input_tokens",
			},
			wantCanonical: &relaybiz.CanonicalUsage{UncachedInputTokens: 130, CacheReadTokens: 45056, OutputTokens: 9},
		},
		{
			name:          "conflicting protocol fields are ambiguous",
			body:          `{"usage":{"prompt_tokens":10,"completion_tokens":5,"cache_read_input_tokens":2,"prompt_tokens_details":{"cached_tokens":2}}}`,
			wantStatus:    relaybiz.UsageParseAmbiguous,
			wantSemantics: "",
			wantReported: relaybiz.ReportedUsage{
				PromptTokens: 10, OutputTokens: 5, CacheReadTokens: 2,
				SourceProtocol: "anthropic_messages", FieldShape: "prompt_tokens+cache_read_input_tokens",
			},
			wantCanonical: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractEnvelopeFromJSON([]byte(tc.body), tc.fallback)
			if got.ParseStatus != tc.wantStatus {
				t.Fatalf("ParseStatus = %q, want %q (env %+v)", got.ParseStatus, tc.wantStatus, got)
			}
			if got.Semantics != tc.wantSemantics {
				t.Fatalf("Semantics = %q, want %q", got.Semantics, tc.wantSemantics)
			}
			if got.Reported != tc.wantReported {
				t.Fatalf("Reported = %+v, want %+v", got.Reported, tc.wantReported)
			}
			if tc.wantCanonical == nil {
				if got.Canonical != nil {
					t.Fatalf("Canonical = %+v, want nil (ambiguous must not fabricate)", got.Canonical)
				}
			} else {
				if got.Canonical == nil || *got.Canonical != *tc.wantCanonical {
					t.Fatalf("Canonical = %+v, want %+v", got.Canonical, tc.wantCanonical)
				}
			}
			if got.ParseStatus == relaybiz.UsageParseAmbiguous {
				if got.SubsetCandidate == nil || got.ExclusiveCandidate == nil {
					t.Fatal("ambiguous envelope must carry both candidates")
				}
				if got.DecisionReason == "" {
					t.Fatal("ambiguous envelope must record a decision reason")
				}
			}
		})
	}
}

func TestDecideEnvelope_InvalidBucketsAreAmbiguous(t *testing.T) {
	t.Run("negative reported bucket", func(t *testing.T) {
		got := ExtractEnvelopeFromJSON([]byte(`{"usage":{"prompt_tokens":-1,"completion_tokens":2}}`), 0)
		if got.ParseStatus != relaybiz.UsageParseAmbiguous || got.DecisionReason != relaybiz.UsageReasonNegativeBucket {
			t.Fatalf("got status=%q reason=%q", got.ParseStatus, got.DecisionReason)
		}
		if got.Reported.PromptTokens != -1 {
			t.Fatalf("reported prompt was sanitized before audit: %d", got.Reported.PromptTokens)
		}
	})

	t.Run("canonical total overflow", func(t *testing.T) {
		got := DecideEnvelope(relaybiz.ReportedUsage{
			PromptTokens: math.MaxInt64,
			OutputTokens: 1,
		}, FieldShapeSignals{HasPromptTokens: true}, 0)
		if got.ParseStatus != relaybiz.UsageParseAmbiguous || got.DecisionReason != relaybiz.UsageReasonOverflow {
			t.Fatalf("got status=%q reason=%q", got.ParseStatus, got.DecisionReason)
		}
	})
}

func TestExtractEnvelopeFromJSON_NestedUsage(t *testing.T) {
	body := `{"object":"response","usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10,"input_tokens_details":{"cached_tokens":1,"cache_creation_5m_tokens":2,"cache_creation_1h_tokens":1}}}`
	got := ExtractEnvelopeFromJSON([]byte(body), 0)
	if got.ParseStatus != relaybiz.UsageParseVerified || got.Semantics != relaybiz.UsageSemanticsOpenAISubset {
		t.Fatalf("got status=%q semantics=%q, want verified subset", got.ParseStatus, got.Semantics)
	}
	want := relaybiz.CanonicalUsage{UncachedInputTokens: 7, CacheReadTokens: 1, CacheCreation5mTokens: 2, CacheCreation1hTokens: 1, OutputTokens: 2}
	if got.Canonical == nil || *got.Canonical != want {
		t.Fatalf("Canonical = %+v, want %+v", got.Canonical, want)
	}
	if got.BillableTotal() != 13 {
		t.Fatalf("BillableTotal = %d, want 13", got.BillableTotal())
	}
}

func TestCanonicalUsageIsEmpty(t *testing.T) {
	if !(relaybiz.CanonicalUsage{}).IsEmpty() {
		t.Fatal("expected empty usage")
	}
	if (relaybiz.CanonicalUsage{OutputTokens: 1}).IsEmpty() {
		t.Fatal("expected non-empty usage")
	}
}

func TestCanonicalUsageBillableTotal(t *testing.T) {
	u := relaybiz.CanonicalUsage{
		UncachedInputTokens:   1,
		OutputTokens:          2,
		CacheReadTokens:       3,
		CacheCreation5mTokens: 4,
		CacheCreation1hTokens: 5,
	}
	if got, want := u.BillableTotal(), int64(15); got != want {
		t.Fatalf("BillableTotal() = %d, want %d", got, want)
	}
}
