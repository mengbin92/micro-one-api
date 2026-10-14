package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/repair"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/contractdto"
)

func TestOfflineCandidateCLIProducesReviewOnlyBundleAndNeverOverwrites(t *testing.T) {
	out := filepath.Join(t.TempDir(), "review.json")
	args := []string{"-proposal", "../../domain/upstream/repair/testdata/finite-proposal.json", "-fixtures", "../../domain/upstream/repair/testdata/independent-fixtures.json", "-out", out}
	require.Equal(t, 0, execute(args))
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var bundle repair.Bundle
	require.NoError(t, jsonx.Unmarshal(raw, &bundle))
	require.Equal(t, "review_required", bundle.Status)
	require.Zero(t, bundle.Failures)
	require.Equal(t, 2, execute(args))
	unchanged, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, raw, unchanged)
	require.Equal(t, 2, execute(append(args, "-review-key", "/must-not-access")))
	require.Equal(t, 2, execute(append(args, "-action", "review")), "replay alone cannot sign a publication")
}

func TestIndependentReviewerProducesContentBoundOwnerRequest(t *testing.T) {
	dir := t.TempDir()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "reviewer-private")
	require.NoError(t, os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key)), 0600))
	out := filepath.Join(dir, "create.json")
	args := []string{"-action", "review", "-proposal", "../../domain/upstream/repair/testdata/finite-proposal.json", "-fixtures", "../../domain/upstream/repair/testdata/independent-fixtures.json", "-out", out, "-review-key", keyPath, "-key-id", "reviewer", "-reviewer", "independent-human", "-ci-evidence", "fixed-ci", "-reason", "reviewed-equivalence", "-expires-at", fmt.Sprint(time.Now().Add(time.Hour).Unix())}
	require.Equal(t, 0, execute(args))
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var request v.CreateUpstreamContractRequest
	require.NoError(t, protojson.Unmarshal(raw, &request))
	d := contractdto.Document(request.Document)
	require.NoError(t, contract.VerifyRepairReview(d, map[string]string{"reviewer": base64.StdEncoding.EncodeToString(public)}, time.Now()))
	require.Equal(t, "reviewed-equivalence", request.Reason)
	require.Equal(t, "independent-human", d.Repair.Reviewer)
}
