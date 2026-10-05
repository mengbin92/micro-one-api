// iam-migrate is the sole offline identity-owner cutover entrypoint. DSNs and
// trust keys are explicitly provisioned; no production service DSN fallback.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/xdb"
)

type signedEvidence struct {
	Payload   jsonx.RawMessage
	Signature string
}

// Set by the reviewed local build using -ldflags '-X main.sourceDigest=…'.
var sourceDigest string

func decodeStrict(raw []byte, out any) error {
	d := jsonx.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func loadEvidence(path, keyPath string) (*biz.IAMCutoverEvidence, error) {
	if path == "" || keyPath == "" {
		return nil, fmt.Errorf("signed evidence and provisioned trust key are required")
	}
	raw, err := os.ReadFile(path) // #nosec G304 G703 -- Offline operator chooses the signed evidence file; no API request controls this path.
	if err != nil {
		return nil, err
	}
	var envelope signedEvidence
	if err = decodeStrict(raw, &envelope); err != nil {
		return nil, err
	}
	keyRaw, err := os.ReadFile(keyPath) // #nosec G304 G703 -- Trust key path is provisioned by the offline operator, never an API input.
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyRaw)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid migration trust key")
	}
	sig, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), envelope.Payload, sig) {
		return nil, fmt.Errorf("cutover evidence signature rejected")
	}
	var evidence biz.IAMCutoverEvidence
	if err = decodeStrict(envelope.Payload, &evidence); err != nil {
		return nil, err
	}
	return &evidence, nil
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: iam-migrate status|inventory|apply|shadow|block|rebuild|import|verify|activate|complete|resume|manifest-digest|keygen|sign-evidence [flags]")
	}
	if args[0] == "manifest-digest" {
		f := flag.NewFlagSet(args[0], flag.ContinueOnError)
		path := f.String("manifest", "", "reviewed manifest JSON")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" || len(f.Args()) != 0 {
			return fmt.Errorf("manifest path required")
		}
		raw, err := os.ReadFile(*path) // #nosec G703 -- Explicit local CLI manifest input; arbitrary operator-selected paths are intentional.
		if err != nil {
			return err
		}
		var manifest biz.IAMMigrationManifest
		if err = decodeStrict(raw, &manifest); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, biz.IAMMigrationDigest(manifest))
		return err
	}
	if args[0] == "keygen" || args[0] == "sign-evidence" {
		return approvalCommand(args, stdout)
	}
	f := flag.NewFlagSet("iam-migrate "+args[0], flag.ContinueOnError)
	batch := f.String("batch", "", "migration batch ID")
	reason := f.String("reason", "", "audit reason")
	requestID := f.String("request-id", "", "stable unique request ID; replay returns its committed receipt")
	revision := f.Uint64("expected-policy-revision", 0, "policy CAS from status")
	evidenceFile := f.String("evidence", "", "root-approved signed evidence JSON")
	manifestFile := f.String("manifest", "", "optional root-approved manifest JSON")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if len(f.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	req := biz.IAMMigrationRequest{Command: args[0], BatchID: *batch, Reason: *reason, RequestID: *requestID, ExpectedPolicyRevision: *revision}
	if *manifestFile != "" {
		raw, err := os.ReadFile(*manifestFile) // #nosec G703 -- Explicit local CLI manifest input; no remote request can select files.
		if err != nil {
			return err
		}
		if err = decodeStrict(raw, &req.Manifest); err != nil {
			return err
		}
	}
	if *evidenceFile != "" {
		e, err := loadEvidence(*evidenceFile, os.Getenv("IAM_MIGRATION_TRUST_KEY_FILE"))
		if err != nil {
			return err
		}
		req.Evidence = e
	}
	dsn := os.Getenv("IAM_MIGRATION_DSN")
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("IAM_MIGRATION_DSN dedicated channel is required; service DSNs are never used")
	}
	databaseIdentity := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(dsn+"\x00"+os.Getenv("IAM_MIGRATION_SCHEMA"))))
	if req.Evidence != nil && (req.Evidence.DatabaseIdentity != databaseIdentity || len(sourceDigest) != 64 || req.Evidence.SourceDigest != sourceDigest) {
		return fmt.Errorf("signed evidence does not match this migration binary and dedicated database channel")
	}
	repo, runner, close, err := data.OpenIAMMigrationStorage(xdb.DatabaseConfig{DSN: dsn, Driver: os.Getenv("IAM_MIGRATION_DRIVER"), Schema: os.Getenv("IAM_MIGRATION_SCHEMA")})
	if err != nil {
		return fmt.Errorf("open dedicated migration storage failed")
	}
	defer close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := biz.NewIAMMigrationUsecase(repo, runner).Execute(ctx, req)
	report.DatabaseIdentity, report.SourceDigest = databaseIdentity, sourceDigest
	if encodeErr := jsonx.NewEncoder(stdout).Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}

// These offline commands are run by the approving root operator. Provision the
// public trust key through an independent administrative channel. The ordinary
// migration process must never possess the approval private key.
func approvalCommand(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	privateFile := f.String("private-key", "", "private root approval key file")
	publicFile := f.String("public-key", "", "new public trust key file (keygen)")
	payloadFile := f.String("payload", "", "reviewed evidence payload JSON (sign-evidence)")
	output := f.String("output", "", "new signed evidence file (sign-evidence)")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if len(f.Args()) != 0 || *privateFile == "" {
		return fmt.Errorf("private key path required")
	}
	if args[0] == "keygen" {
		if *publicFile == "" || *publicFile == *privateFile {
			return fmt.Errorf("distinct public and private paths required")
		}
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err = writePrivateFile(*privateFile, []byte(base64.StdEncoding.EncodeToString(key))); err != nil {
			return err
		}
		if err = writePrivateFile(*publicFile, []byte(base64.StdEncoding.EncodeToString(pub))); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, "Approval keys created in private files; provision the public trust key independently.")
		return err
	}
	if *payloadFile == "" || *output == "" {
		return fmt.Errorf("payload and output paths required")
	}
	keyRaw, err := os.ReadFile(*privateFile) // #nosec G703 -- Operator-selected signing key; permissions are checked before this read.
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyRaw)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid approval private key")
	}
	payload, err := os.ReadFile(*payloadFile) // #nosec G703 -- Root operator explicitly selects the reviewed local signing payload.
	if err != nil {
		return err
	}
	var evidence biz.IAMCutoverEvidence
	if err = decodeStrict(payload, &evidence); err != nil {
		return err
	}
	// Sign the exact compact bytes stored in RawMessage; signing a pretty input
	// before JSON marshaling could invalidate its signature during compaction.
	payload, err = jsonx.Marshal(evidence)
	if err != nil {
		return err
	}
	envelope, err := jsonx.Marshal(signedEvidence{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(key), payload))})
	if err != nil {
		return err
	}
	if err = writePrivateFile(*output, envelope); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Signed evidence written; private key was not copied to the migration process.")
	return err
}

func writePrivateFile(path string, value []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 G703 -- Offline operator selects a new private output; O_EXCL rejects existing files and symlinks.
	if err != nil {
		return err
	}
	if _, err = f.Write(value); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
