// upstream-repair prepares a bounded agent proposal or signs an independently
// replayed finite contract in a reviewer environment. It never executes sample
// text, patches, tests supplied by a candidate, or production API operations.
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/repair"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/contractdto"
)

func main() { os.Exit(run()) }
func run() int {
	return execute(os.Args[1:])
}
func execute(args []string) int {
	fs := flag.NewFlagSet("upstream-repair", flag.ContinueOnError)
	action := fs.String("action", "prepare", "prepare or review; neither publishes")
	proposalPath := fs.String("proposal", "", "agent proposal JSON, with no executable instructions")
	fixturePath := fs.String("fixtures", "", "independently maintained fixture set JSON")
	outPath := fs.String("out", "", "absolute new review bundle or protobuf JSON create request")
	keyPath := fs.String("review-key", "", "review-only 0600 base64 Ed25519 private key file")
	keyID := fs.String("key-id", "", "configured reviewer public key ID")
	reviewer := fs.String("reviewer", "", "human reviewer identity")
	ciEvidence := fs.String("ci-evidence", "", "independent fixed CI evidence ID")
	reason := fs.String("reason", "", "explicit review reason")
	expires := fs.Int64("expires-at", 0, "review expiration Unix seconds; at most 24 hours")
	if fs.Parse(args) != nil {
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 2 }
	if *action != "prepare" && *action != "review" || !filepath.IsAbs(*outPath) {
		return fail(fmt.Errorf("prepare/review and absolute new output required"))
	}
	if *action == "prepare" && *keyPath != "" {
		return fail(fmt.Errorf("candidate environment must not receive reviewer credentials"))
	}
	var p repair.Proposal
	var fixtures repair.FixtureSet
	if err := readJSON(*proposalPath, &p); err != nil {
		return fail(err)
	}
	if err := readJSON(*fixturePath, &fixtures); err != nil {
		return fail(err)
	}
	bundle, err := repair.Prepare(p, fixtures)
	if err != nil {
		return fail(err)
	}
	encoded, err := jsonx.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fail(err)
	}
	if *action == "review" {
		if bundle.Status != "review_required" || bundle.Failures != 0 || *reviewer == "" || *keyID == "" || *ciEvidence == "" || *reason == "" {
			return fail(fmt.Errorf("passing independent replay, CI evidence, reviewer and reason required"))
		}
		info, err := os.Lstat(*keyPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fail(fmt.Errorf("review key must be a private regular file"))
		}
		raw, err := os.ReadFile(*keyPath)
		if err != nil {
			return fail(fmt.Errorf("review key unavailable"))
		}
		key, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(raw)))
		if err != nil || len(key) != ed25519.PrivateKeySize {
			return fail(fmt.Errorf("invalid reviewer key"))
		}
		d := p.Candidate
		base := ""
		if p.Base != nil {
			base = p.Base.Revision()
		}
		d.Repair = &contract.RepairReview{ProposalID: bundle.ProposalID, BaseRevision: base, BindingRevision: p.BindingRevision, IncidentRevision: p.IncidentRevision, SourceRevision: p.SourceRevision, ReplayID: bundle.ReplayID, CIEvidenceID: *ciEvidence, Reviewer: *reviewer, KeyID: *keyID, ExpiresAt: *expires}
		d, err = contract.SignRepairReview(d, ed25519.PrivateKey(key))
		if err != nil {
			return fail(err)
		}
		public := ed25519.PrivateKey(key).Public().(ed25519.PublicKey)
		if err := contract.VerifyRepairReview(d, map[string]string{*keyID: base64.StdEncoding.EncodeToString(public)}, time.Now()); err != nil {
			return fail(err)
		}
		encoded, err = protojson.MarshalOptions{Indent: "  "}.Marshal(&v.CreateUpstreamContractRequest{Document: contractdto.DocumentProto(d), Reason: *reason})
		if err != nil {
			return fail(err)
		}
	}
	f, err := os.OpenFile(*outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail(err)
	}
	written := false
	defer func() {
		_ = f.Close()
		if !written {
			_ = os.Remove(*outPath)
		}
	}()
	if _, err := f.Write(encoded); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	written = true
	fmt.Printf("repair proposal %s; replay %s; %s; failures=%d\n", bundle.ProposalID, bundle.ReplayID, bundle.Status, bundle.Failures)
	if bundle.Failures > 0 {
		return 1
	}
	return 0
}
func readJSON(path string, out any) error {
	// Scope reads to the operator-selected parent, with no symlink/traversal
	// escape from that directory. Sample/proposal text never selects a path.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return fmt.Errorf("input unavailable or too large")
	}
	if err := jsonx.ValidateUnique(raw); err != nil {
		return err
	}
	d := jsonx.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
