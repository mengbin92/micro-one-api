package server

import (
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/metrics"

	billingv1 "micro-one-api/api/billing/v1"
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

// F8: a commit transport error after the upstream served the request must not
// silently leak settlement. The bounded retry covers transient RPC loss; when
// billing already persisted the commit (response lost after persist), the
// durable attempt state recovers the success path without double-charging.
func TestCommitQuota_RecoversWhenBillingAlreadyCommitted(t *testing.T) {
	billing := &rawBillingClient{
		commitErrs: []error{stderrors.New("transport"), stderrors.New("transport"), stderrors.New("transport")},
		attempts: []*billingv1.RequestAttempt{
			{ReservationId: "res-1", RequestId: "req-1", Status: "committed", ActualCost: 777, CostFieldsVisible: true},
		},
	}
	s := &HTTPServer{billingClient: billing}
	detail := usageLogInput{UserID: 42, RequestID: "req-1", RootRequestID: "root-1"}
	if err := s.commitQuota(context.Background(), "res-1", 100, true, detail); err != nil {
		t.Fatalf("commitQuota should recover from confirmed billing state: %v", err)
	}
	if billing.commits != 3 {
		t.Fatalf("commit attempts = %d, want 3 (bounded retry)", billing.commits)
	}
	if billing.listAttemptsCalls != 1 {
		t.Fatalf("recovery state reads = %d, want 1", billing.listAttemptsCalls)
	}
	got := billing.listAttemptsRequests[0]
	if got.GetUserId() != "42" || got.GetRootRequestId() != "root-1" {
		t.Fatalf("recovery query = %q/%q, want 42/root-1", got.GetUserId(), got.GetRootRequestId())
	}
	// The success path continues with the stored committed amount.
	if len(billing.commitRequests) != 3 {
		t.Fatalf("commit requests = %d, want 3", len(billing.commitRequests))
	}
}

// F8: when billing confirms the reservation is still uncommitted, the failure
// propagates as before instead of being masked.
func TestCommitQuota_UnconfirmedCommitFailurePropagates(t *testing.T) {
	billing := &rawBillingClient{
		commitErrs: []error{stderrors.New("transport"), stderrors.New("transport"), stderrors.New("transport")},
		attempts: []*billingv1.RequestAttempt{
			{ReservationId: "res-1", RequestId: "req-1", Status: "reserved"},
		},
	}
	s := &HTTPServer{billingClient: billing}
	detail := usageLogInput{UserID: 42, RequestID: "req-1", RootRequestID: "root-1"}
	if err := s.commitQuota(context.Background(), "res-1", 100, true, detail); err == nil {
		t.Fatal("commitQuota should propagate the failure when billing state is unconfirmed")
	}
	if billing.commits != 3 || billing.listAttemptsCalls != 1 {
		t.Fatalf("commits=%d state reads=%d, want 3/1", billing.commits, billing.listAttemptsCalls)
	}
}

// F8: a transient transport error recovered by retry alone must not query the
// attempt state at all.
func TestCommitQuota_TransientRetrySkipsStateRecovery(t *testing.T) {
	billing := &rawBillingClient{
		commitErrs: []error{stderrors.New("transport"), nil},
	}
	s := &HTTPServer{billingClient: billing}
	detail := usageLogInput{UserID: 42, RequestID: "req-1", RootRequestID: "root-1"}
	if err := s.commitQuota(context.Background(), "res-1", 100, true, detail); err != nil {
		t.Fatalf("commitQuota: %v", err)
	}
	if billing.commits != 2 {
		t.Fatalf("commit attempts = %d, want 2", billing.commits)
	}
	if billing.listAttemptsCalls != 0 {
		t.Fatalf("recovery state reads = %d, want 0", billing.listAttemptsCalls)
	}
}

// Non-stream orchestrator coverage: the stream finalizer's semantics-missing
// anomaly must also fire for verified non-stream envelopes carrying cache
// buckets, so billing's ambiguous safety path has the same quarantine signal.
func TestRecordFinalUsageSemanticsAnomaly_NonStreamCoverage(t *testing.T) {
	before := testutil.ToFloat64(metrics.TokenUsageParseAnomaly.WithLabelValues(
		relaybiz.UsageReasonFinalAttemptSemanticsMissing))

	recordFinalUsageSemanticsAnomaly(&relaybiz.UsageEnvelope{
		ParseStatus: relaybiz.UsageParseVerified,
		Semantics:   "",
		Canonical:   &relaybiz.CanonicalUsage{CacheReadTokens: 8, OutputTokens: 3},
	})
	recordFinalUsageSemanticsAnomaly(&relaybiz.UsageEnvelope{
		ParseStatus: relaybiz.UsageParseVerified,
		Semantics:   "",
		Canonical:   &relaybiz.CanonicalUsage{CacheCreation5mTokens: 2, OutputTokens: 1},
	})
	// Declared semantics and non-verified envelopes must not fire.
	recordFinalUsageSemanticsAnomaly(&relaybiz.UsageEnvelope{
		ParseStatus: relaybiz.UsageParseVerified,
		Semantics:   relaybiz.UsageSemanticsAnthropicExclusive,
		Canonical:   &relaybiz.CanonicalUsage{CacheReadTokens: 8, OutputTokens: 3},
	})
	recordFinalUsageSemanticsAnomaly(&relaybiz.UsageEnvelope{
		ParseStatus: relaybiz.UsageParseEstimated,
		Canonical:   &relaybiz.CanonicalUsage{CacheReadTokens: 8, OutputTokens: 3},
	})
	recordFinalUsageSemanticsAnomaly(nil)

	if got := testutil.ToFloat64(metrics.TokenUsageParseAnomaly.WithLabelValues(
		relaybiz.UsageReasonFinalAttemptSemanticsMissing)); got != before+2 {
		t.Fatalf("semantics-missing anomaly = %v, want %v", got, before+2)
	}
}

// F8: billing blanks cost columns when the caller lacks the
// billing.account.cost.read scope. Synthesizing a success with a zero amount
// would silently skip subscription/session-window accounting, so recovery
// must stay conservative and expose the reason in its state token.
func TestCommitQuota_CommittedButCostHiddenStaysConservative(t *testing.T) {
	transport := stderrors.New("transport")
	billing := &rawBillingClient{
		commitErrs: []error{transport, transport, transport},
		attempts: []*billingv1.RequestAttempt{
			{ReservationId: "res-1", RequestId: "req-1", Status: "committed", ActualCost: 777, CostFieldsVisible: false},
		},
	}
	s := &HTTPServer{billingClient: billing}
	detail := usageLogInput{UserID: 42, RequestID: "req-1", RootRequestID: "root-1"}
	if err := s.commitQuota(context.Background(), "res-1", 100, true, detail); err == nil {
		t.Fatal("commitQuota must stay conservative when the committed amount is hidden")
	}
	recovered, state := s.recoverCommittedQuota(context.Background(), "res-1", detail)
	if recovered != nil {
		t.Fatalf("hidden cost produced a synthetic success with amount %d", recovered.GetCommittedAmount())
	}
	if state != "committed_cost_hidden" {
		t.Fatalf("recovery state = %q, want committed_cost_hidden", state)
	}
}

// F8: legacy details without a root id must still find the reservation, and
// the synthetic response must carry billing's stored actual_cost (not the
// relay's token estimate) so downstream accounting stays authoritative.
func TestCommitQuota_RecoveryFallsBackToRequestIDAndUsesStoredAmount(t *testing.T) {
	billing := &rawBillingClient{
		attempts: []*billingv1.RequestAttempt{
			{ReservationId: "res-1", RequestId: "req-1", Status: "committed", ActualCost: 512, CostFieldsVisible: true},
		},
	}
	s := &HTTPServer{billingClient: billing}
	detail := usageLogInput{UserID: 7, RequestID: "req-1"}
	recovered, state := s.recoverCommittedQuota(context.Background(), "res-1", detail)
	if recovered == nil || !recovered.GetSuccess() {
		t.Fatalf("recovery failed, state = %q", state)
	}
	if got := recovered.GetCommittedAmount(); got != 512 {
		t.Fatalf("synthetic committed_amount = %d, want stored actual_cost 512", got)
	}
	if got := billing.listAttemptsRequests[0].GetRootRequestId(); got != "req-1" {
		t.Fatalf("recovery root_request_id = %q, want req-1 fallback", got)
	}
}

// F8: billing orders attempts by attempt_number ASC. A reservation beyond the
// first page must still be recoverable with a bounded number of state reads.
func TestCommitQuota_RecoveryPaginatesToFindReservation(t *testing.T) {
	attempts := make([]*billingv1.RequestAttempt, 101)
	for i := range attempts {
		attempts[i] = &billingv1.RequestAttempt{ReservationId: "other", Status: "reserved"}
	}
	attempts[100] = &billingv1.RequestAttempt{ReservationId: "res-target", Status: "committed", ActualCost: 321, CostFieldsVisible: true}
	billing := &rawBillingClient{attempts: attempts}
	s := &HTTPServer{billingClient: billing}
	detail := usageLogInput{UserID: 42, RequestID: "req-1", RootRequestID: "root-1"}
	recovered, state := s.recoverCommittedQuota(context.Background(), "res-target", detail)
	if recovered == nil || recovered.GetCommittedAmount() != 321 {
		t.Fatalf("second-page reservation not recovered, state = %q", state)
	}
	if billing.listAttemptsCalls != 2 {
		t.Fatalf("state reads = %d, want 2 (bounded pagination)", billing.listAttemptsCalls)
	}
	if got := billing.listAttemptsRequests[1].GetPage(); got != 2 {
		t.Fatalf("second read page = %d, want 2", got)
	}
}

// F8: a recovered commit emits exactly one quota outcome, so the outcome
// counter remains a true settlement denominator.
func TestCommitQuota_RecoveredCommitRecordsSingleOutcome(t *testing.T) {
	observation := &relayExecutionObservation{
		endpoint: relayEndpointChatCompletions,
		stream:   "false",
		path:     relayExecutionPathOrchestrator,
	}
	ctx := context.WithValue(context.Background(), relayExecutionObservationKey{}, observation)
	recoveredLabel := metrics.RelayExecutorQuotaOutcomeTotal.WithLabelValues(
		relayEndpointChatCompletions, "false", relayExecutionPathOrchestrator, "commit_recovered")
	successLabel := metrics.RelayExecutorQuotaOutcomeTotal.WithLabelValues(
		relayEndpointChatCompletions, "false", relayExecutionPathOrchestrator, "commit_success")
	recoveredBefore := testutil.ToFloat64(recoveredLabel)
	successBefore := testutil.ToFloat64(successLabel)

	transport := stderrors.New("transport")
	billing := &rawBillingClient{
		commitErrs: []error{transport, transport, transport},
		attempts: []*billingv1.RequestAttempt{
			{ReservationId: "res-1", Status: "committed", ActualCost: 777, CostFieldsVisible: true},
		},
	}
	s := &HTTPServer{billingClient: billing}
	if err := s.commitQuota(ctx, "res-1", 100, true, usageLogInput{UserID: 42, RequestID: "req-1", RootRequestID: "root-1"}); err != nil {
		t.Fatalf("commitQuota: %v", err)
	}
	if got := testutil.ToFloat64(recoveredLabel); got != recoveredBefore+1 {
		t.Fatalf("commit_recovered outcome = %v, want %v", got, recoveredBefore+1)
	}
	if got := testutil.ToFloat64(successLabel); got != successBefore {
		t.Fatalf("commit_success outcome = %v, want unchanged %v", got, successBefore)
	}
}

// F8: every non-recovered branch returns a non-empty state token so the
// operator log can be triaged without re-querying billing.
func TestCommitQuota_RecoveryStatesAreDiagnosable(t *testing.T) {
	s := &HTTPServer{billingClient: &rawBillingClient{}}
	tests := []struct {
		name   string
		detail []usageLogInput
		want   string
	}{
		{name: "no usage detail", want: "recovery_unavailable"},
		{name: "missing identity", detail: []usageLogInput{{}}, want: "recovery_identity_missing"},
		{name: "reservation absent", detail: []usageLogInput{{UserID: 42, RequestID: "req-1"}}, want: "reservation_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, state := s.recoverCommittedQuota(context.Background(), "res-1", tt.detail...)
			if state != tt.want {
				t.Fatalf("recovery state = %q, want %q", state, tt.want)
			}
		})
	}
}
