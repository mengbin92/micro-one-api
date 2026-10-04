package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func TestIAMMigrationEvidenceSignature(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "trust-key")
	evidenceFile := filepath.Join(dir, "evidence.json")
	require.NoError(t, os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(pub)), 0600))
	payload, err := jsonx.Marshal(biz.IAMCutoverEvidence{BatchID: "root-approved-batch", RootUserID: 42})
	require.NoError(t, err)
	write := func(payload []byte, signature []byte) {
		raw, err := jsonx.Marshal(signedEvidence{Payload: payload, Signature: base64.StdEncoding.EncodeToString(signature)})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(evidenceFile, raw, 0600))
	}
	signature := ed25519.Sign(private, payload)
	write(payload, signature)
	e, err := loadEvidence(evidenceFile, keyFile)
	require.NoError(t, err)
	require.Equal(t, int64(42), e.RootUserID)
	changed := bytes.Replace(payload, []byte("42"), []byte("43"), 1)
	write(changed, signature)
	_, err = loadEvidence(evidenceFile, keyFile)
	require.ErrorContains(t, err, "signature rejected")
	_, err = loadEvidence("", keyFile)
	require.Error(t, err)
	unknown := []byte(`{"BatchID":"root-approved-batch","not_a_real_field":true}`)
	write(unknown, ed25519.Sign(private, unknown))
	_, err = loadEvidence(evidenceFile, keyFile)
	require.Error(t, err)
}

func TestIAMMigrationCLINeverFallsBackToServiceDSN(t *testing.T) {
	t.Setenv("IAM_MIGRATION_DSN", "")
	t.Setenv("IDENTITY_SQL_DSN", "service-production-dsn-must-not-be-used")
	t.Setenv("SQL_DSN", "global-production-dsn-must-not-be-used")
	err := run([]string{"inventory"}, &bytes.Buffer{})
	require.ErrorContains(t, err, "dedicated channel is required")
}

func TestIAMMigrationStrictInput(t *testing.T) {
	var out biz.IAMMigrationManifest
	require.Error(t, decodeStrict([]byte(`{} {}`), &out))
	require.Error(t, decodeStrict([]byte(`{"unknown":true}`), &out))
}

func TestIAMMigrationManifestDigestOffline(t *testing.T) {
	t.Setenv("IAM_MIGRATION_DSN", "")
	path := filepath.Join(t.TempDir(), "manifest.json")
	require.NoError(t, os.WriteFile(path, []byte("{\n \"Roles\": null, \"Assignments\": null, \"Delegations\": null\n}"), 0600))
	var out bytes.Buffer
	require.NoError(t, run([]string{"manifest-digest", "-manifest", path}, &out))
	require.Equal(t, biz.IAMMigrationDigest(biz.IAMMigrationManifest{}), strings.TrimSpace(out.String()))
	require.NoError(t, os.WriteFile(path, []byte(`{"unknown":true}`), 0600))
	require.Error(t, run([]string{"manifest-digest", "-manifest", path}, &bytes.Buffer{}))
}

func TestIAMMigrationApprovalCommands(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, "private")
	public := filepath.Join(dir, "public")
	payload := filepath.Join(dir, "payload.json")
	out := filepath.Join(dir, "signed.json")
	require.NoError(t, run([]string{"keygen", "-private-key", private, "-public-key", public}, &bytes.Buffer{}))
	info, err := os.Stat(private)
	require.NoError(t, err)
	require.EqualValues(t, 0600, info.Mode().Perm())
	require.Error(t, run([]string{"keygen", "-private-key", private, "-public-key", public}, &bytes.Buffer{}))
	require.NoError(t, os.WriteFile(payload, []byte("{\n  \"RootUserID\": 42,\n  \"BatchID\": \"reviewed\"\n}\n"), 0600))
	require.NoError(t, run([]string{"sign-evidence", "-private-key", private, "-payload", payload, "-output", out}, &bytes.Buffer{}))
	e, err := loadEvidence(out, public)
	require.NoError(t, err)
	require.Equal(t, int64(42), e.RootUserID)
	require.Equal(t, "reviewed", e.BatchID)
}
