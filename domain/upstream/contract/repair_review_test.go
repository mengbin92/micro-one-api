package contract

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRepairReviewBindsContentScopeAndIndependentEvidence(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now().UTC()
	d := doc()
	d.Repair = &RepairReview{ProposalID: strings.Repeat("a", 64), SourceRevision: "source-1", ReplayID: strings.Repeat("b", 64), CIEvidenceID: "independent-ci", Reviewer: "operator", KeyID: "reviewer-1", ExpiresAt: now.Add(time.Hour).Unix()}
	keys := map[string]string{"reviewer-1": base64.StdEncoding.EncodeToString(pub)}
	require.Error(t, VerifyRepairReview(d, keys, now))
	signed, err := SignRepairReview(d, key)
	require.NoError(t, err)
	require.NoError(t, VerifyRepairReview(signed, keys, now))
	require.Empty(t, d.Repair.Signature, "signing cannot mutate a shared candidate")
	require.Error(t, VerifyRepairReview(signed, map[string]string{}, now))
	require.Error(t, VerifyRepairReview(signed, keys, now.Add(2*time.Hour)))
	for _, change := range []func(*Document){
		func(d *Document) { d.Scope.ID++ },
		func(d *Document) { d.Scope.Model = "vendor/exact" },
		func(d *Document) { d.Repair.IncidentRevision++ },
		func(d *Document) { d.Repair.BindingRevision++ },
		func(d *Document) { d.Repair.SourceRevision = "new-credential" },
		func(d *Document) { d.Repair.ReplayID = strings.Repeat("c", 64) },
		func(d *Document) { d.FixtureEvidence = "self-authored-test" },
	} {
		copy := signed
		review := *signed.Repair
		copy.Repair = &review
		change(&copy)
		require.Error(t, VerifyRepairReview(copy, keys, now))
	}
}
