package repair

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/pkg/jsonx"
)

func proposalFixture() (Proposal, FixtureSet) {
	d := contract.Document{Scope: contract.Scope{Kind: "channel", ID: 1, Model: "vendor/Exact", Protocol: "openai_chat"}, Provider: "fixture", AdapterBuild: contract.BuildID, ParserRevision: contract.ParserID, UsageRevision: contract.UsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "independent-matrix", BasisRevision: "schema-v2", Sets: []contract.RuleSet{{Level: "model", Rules: []contract.Rule{{ID: "rename", Kind: "rename", Path: "old_option", Target: "new_option", Evidence: "equivalence"}}}}}
	p := Proposal{Candidate: d, IncidentID: "incident", Classification: "protocol_drift", ContractProbeFailed: true, SourceRevision: "source", EvidenceIDs: []string{"redacted-usage"}, BasisDigest: strings.Repeat("a", 64)}
	fixtures := FixtureSet{Authority: "independent-regression", BasisDigest: p.BasisDigest, Scope: d.Scope, Fixtures: []Fixture{
		{ID: "zero-preserved", Request: jsonx.RawMessage(`{"old_option":0,"unknown":true}`), ExpectedRequest: jsonx.RawMessage(`{"new_option":0,"unknown":true}`), Response: jsonx.RawMessage(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), Status: "verified", Buckets: &[5]int64{}},
		{ID: "conflict-rejected", Request: jsonx.RawMessage(`{"old_option":1,"new_option":2}`), Reject: true},
		{ID: "stream-duplicate-terminal", Request: jsonx.RawMessage(`{"old_option":null}`), ExpectedRequest: jsonx.RawMessage(`{"new_option":null}`), Frames: []jsonx.RawMessage{jsonx.RawMessage(`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), jsonx.RawMessage(`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)}, Status: "verified", Complete: true, Buckets: &[5]int64{1, 0, 0, 0, 1}},
		{ID: "unsupported-dimension", Request: jsonx.RawMessage(`{}`), ExpectedRequest: jsonx.RawMessage(`{}`), Response: jsonx.RawMessage(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"extra_billable_tokens":10}}`), Status: "unsupported"},
	}}
	return p, fixtures
}

func TestCandidateIndependentReplayAndNoRuntimeEffect(t *testing.T) {
	p, f := proposalFixture()
	b, err := Prepare(p, f)
	require.NoError(t, err)
	require.Zero(t, b.Failures)
	require.Equal(t, "review_required", b.Status)
	require.Len(t, b.ReplayID, 64)
	require.Nil(t, b.Proposal.Candidate.Repair, "offline tests never mint a publication signature")
	f.Fixtures[0].ExpectedRequest = jsonx.RawMessage(`{"new_option":99}`)
	failed, err := Prepare(p, f)
	require.NoError(t, err)
	require.Positive(t, failed.Failures)
	require.Equal(t, "replay_failed", failed.Status)
	require.NotEqual(t, b.ReplayID, failed.ReplayID)
}
func TestClientErrorsAndUntrustedCandidateCannotAuthorizeRepair(t *testing.T) {
	p, f := proposalFixture()
	p.Classification = "client_invalid_input"
	_, err := Prepare(p, f)
	require.Error(t, err)
	p.Classification = "protocol_drift"
	p.ContractProbeFailed = false
	_, err = Prepare(p, f)
	require.Error(t, err)
	p.ContractProbeFailed = true
	p.Candidate.Repair = &contract.RepairReview{Signature: "sample-instruction"}
	_, err = Prepare(p, f)
	require.Error(t, err)
	p.Candidate.Repair = nil
	f.Authority = ""
	_, err = Prepare(p, f)
	require.Error(t, err)
}
func TestNovelPatchRemainsUnexecutedReviewArtifact(t *testing.T) {
	p, f := proposalFixture()
	base := p.Candidate
	p.Base = &base
	p.Patch = &PatchArtifact{BaseCommit: strings.Repeat("a", 40), BuildID: "new-reviewed-build", Diff: "diff --git a/internal/adaptor/example.go b/internal/adaptor/example.go\n--- a/internal/adaptor/example.go\n+++ b/internal/adaptor/example.go\n@@ -1 +1 @@\n-old\n+candidate\n"}
	b, err := Prepare(p, f)
	require.NoError(t, err)
	require.Equal(t, "requires_code_review_and_ci_build", b.Status)
	require.Empty(t, b.ReplayID)
	p.Patch.Diff = strings.ReplaceAll(p.Patch.Diff, "internal/adaptor/example.go", "app/billing/internal/biz/billing.go")
	_, err = Prepare(p, f)
	require.Error(t, err)
}
