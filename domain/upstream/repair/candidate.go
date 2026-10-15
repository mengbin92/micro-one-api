// Package repair prepares offline, bounded candidates. It has no execution,
// network, production credential, ledger or publication capabilities.
package repair

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"

	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
)

type Proposal struct {
	Patch               *PatchArtifact     `json:"patch,omitempty"`
	Base                *contract.Document `json:"base,omitempty"`
	Candidate           contract.Document  `json:"candidate"`
	IncidentID          string             `json:"incident_id"`
	IncidentRevision    int64              `json:"incident_revision"`
	BindingRevision     int64              `json:"binding_revision"`
	SourceRevision      string             `json:"source_revision"`
	Classification      string             `json:"classification"`
	ContractProbeFailed bool               `json:"contract_probe_failed"`
	EvidenceIDs         []string           `json:"evidence_ids"`
	// The official schema is pinned evidence, never an instruction or fetched
	// URL. Novel code patches remain external review artifacts; this tool only
	// replays operations installed in the current runtime.
	BasisDigest string `json:"basis_digest"`
}

type PatchArtifact struct {
	BaseCommit string `json:"base_commit"`
	BuildID    string `json:"build_id"`
	Diff       string `json:"diff"`
}

func validatePatch(p PatchArtifact) error {
	if len(p.BaseCommit) != 40 || p.BuildID == "" || len(p.Diff) == 0 || len(p.Diff) > 256<<10 {
		return fmt.Errorf("bounded patch, fixed base commit and new build required")
	}
	if _, err := hex.DecodeString(p.BaseCommit); err != nil {
		return fmt.Errorf("invalid patch base commit")
	}
	files := 0
	current := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(p.Diff, "\n") {
		if strings.HasPrefix(line, "--- ") && line != "--- a/"+current && line != "--- /dev/null" || strings.HasPrefix(line, "+++ ") && line != "+++ b/"+current {
			return fmt.Errorf("patch header differs from reviewed path")
		}
		if strings.HasPrefix(line, "new file mode ") && line != "new file mode 100644" || strings.HasPrefix(line, "old mode ") || strings.HasPrefix(line, "new mode ") || strings.HasPrefix(line, "rename ") || strings.HasPrefix(line, "copy ") || strings.HasPrefix(line, "GIT binary patch") {
			return fmt.Errorf("patch mode, rename and binary operations require a separate review")
		}
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 4 || !strings.HasPrefix(parts[2], "a/") || !strings.HasPrefix(parts[3], "b/") || parts[2][2:] != parts[3][2:] {
			return fmt.Errorf("exact same-path patch required")
		}
		path := parts[2][2:]
		if strings.Contains(path, "..") || strings.ContainsAny(path, "\\\x00") || !strings.HasSuffix(path, ".go") || !(strings.HasPrefix(path, "internal/adaptor/") || strings.HasPrefix(path, "internal/apicompat/") || strings.HasPrefix(path, "internal/server/usage/") || strings.HasPrefix(path, "domain/upstream/evidence/")) {
			return fmt.Errorf("patch exceeds adapter/parser candidate workspace")
		}
		if seen[path] {
			return fmt.Errorf("duplicate patch file")
		}
		seen[path] = true
		current = path
		files++
	}
	if files == 0 || files > 16 {
		return fmt.Errorf("patch file budget exceeded")
	}
	return nil
}

type Fixture struct {
	ID              string           `json:"id"`
	Request         jsonx.RawMessage `json:"request"`
	ExpectedRequest jsonx.RawMessage `json:"expected_request,omitempty"`
	Reject          bool             `json:"reject"`
	Response        jsonx.RawMessage `json:"response,omitempty"`
	// Frames are the actual SSE data payloads. A JSON string "[DONE]"
	// records Chat's non-JSON protocol terminator in the fixture document.
	Frames   []jsonx.RawMessage `json:"frames,omitempty"`
	Status   string             `json:"status"`
	Terminal string             `json:"terminal,omitempty"`
	Complete bool               `json:"complete"`
	Buckets  *[5]int64          `json:"buckets,omitempty"`
}

type FixtureSet struct {
	Authority   string         `json:"authority"`
	BasisDigest string         `json:"basis_digest"`
	Scope       contract.Scope `json:"scope"`
	Fixtures    []Fixture      `json:"fixtures"`
}

type Result struct {
	ID           string   `json:"id"`
	Passed       bool     `json:"passed"`
	BaseRejected bool     `json:"base_rejected"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
}

type Bundle struct {
	Proposal      Proposal `json:"proposal"`
	ProposalID    string   `json:"proposal_id"`
	FixtureDigest string   `json:"fixture_digest"`
	ReplayID      string   `json:"replay_id"`
	Results       []Result `json:"results"`
	Status        string   `json:"status"`
	Failures      int      `json:"failures"`
}

func digest(v any) string { b, _ := jsonx.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }

// Prepare accepts the independent fixture set separately from agent output.
// Its report never includes request values, response content or error text.
func Prepare(p Proposal, fixtures FixtureSet) (Bundle, error) {
	out := Bundle{Proposal: p, ProposalID: digest(p), FixtureDigest: digest(fixtures), Status: "review_required"}
	if p.Classification != "protocol_drift" || !p.ContractProbeFailed || p.IncidentID == "" || p.IncidentRevision < 0 || p.BindingRevision < 0 || p.SourceRevision == "" || len(p.EvidenceIDs) == 0 || len(p.EvidenceIDs) > 32 || len(p.BasisDigest) != 64 || p.Candidate.Repair != nil {
		return out, fmt.Errorf("validated protocol incident, pinned basis and unsigned bounded candidate required")
	}
	if fixtures.Authority == "" || fixtures.BasisDigest != p.BasisDigest || fixtures.Scope != p.Candidate.Scope || len(fixtures.Fixtures) < 2 || len(fixtures.Fixtures) > 256 {
		return out, fmt.Errorf("independent same-scope fixture authority/basis required")
	}
	if p.Patch != nil {
		if err := validatePatch(*p.Patch); err != nil {
			return out, err
		}
		if p.Candidate.Scope.Validate() != nil || p.Base == nil || p.Base.Scope != p.Candidate.Scope || p.Candidate.Endpoint != p.Base.Endpoint || p.Candidate.APIVersion != p.Base.APIVersion || p.Candidate.Provider != p.Base.Provider || !reflect.DeepEqual(p.Candidate.Inbound, p.Base.Inbound) {
			return out, fmt.Errorf("patch requires fixed base and unchanged source/destination/capability scope")
		}
		out.Status = "requires_code_review_and_ci_build"
		return out, nil
	}
	candidate, err := contract.Compile(p.Candidate)
	if err != nil {
		return out, err
	}
	var base *contract.Snapshot
	if p.Base != nil {
		if p.Base.Scope != p.Candidate.Scope || p.Base.Provider != p.Candidate.Provider || p.Base.Endpoint != p.Candidate.Endpoint || p.Base.APIVersion != p.Candidate.APIVersion || !reflect.DeepEqual(p.Base.Inbound, p.Candidate.Inbound) {
			return out, fmt.Errorf("repair cannot change source, destination or protocol capability scope")
		}
		b, err := contract.Compile(*p.Base)
		if err != nil {
			return out, err
		}
		base = &b
	}
	seen := map[string]bool{}
	positive, negative, streaming := false, false, false
	for _, f := range fixtures.Fixtures {
		if f.ID == "" || seen[f.ID] || len(f.Frames) > 64 || len(f.Response) > evidence.MaxFrameBytes || jsonx.ValidateUnique(f.Request) != nil {
			return out, fmt.Errorf("invalid independent fixture identity, request or budget")
		}
		seen[f.ID] = true
		actual, trace, applyErr := contract.Apply(candidate, f.Request)
		r := Result{ID: f.ID, Passed: f.Reject == (applyErr != nil), ChangedPaths: trace.Paths}
		if base != nil {
			_, _, baseErr := contract.Apply(*base, f.Request)
			r.BaseRejected = baseErr != nil
		}
		if f.Reject {
			negative = true
		} else {
			if jsonx.ValidateUnique(f.ExpectedRequest) != nil {
				return out, fmt.Errorf("expected request must be independent valid JSON")
			}
			r.Passed = r.Passed && sameJSON(actual, f.ExpectedRequest)
			var observation evidence.Observation
			complete := false
			if len(f.Frames) > 0 {
				tracker := evidence.NewTracker(p.Candidate.Scope.Protocol)
				for _, frame := range f.Frames {
					if len(frame) > evidence.MaxFrameBytes {
						return out, fmt.Errorf("fixture frame budget exceeded")
					}
					payload := frame
					var sentinel string
					if p.Candidate.Scope.Protocol == "openai_chat" && jsonx.Unmarshal(frame, &sentinel) == nil && sentinel == "[DONE]" {
						payload = []byte(sentinel)
					}
					// Replay through the live SSE framing seam. ObserveFrame
					// alone cannot distinguish a truncated Chat stream from
					// a response that actually received [DONE].
					var event bytes.Buffer
					for _, line := range bytes.Split(payload, []byte{'\n'}) {
						event.WriteString("data: ")
						event.Write(line)
						event.WriteByte('\n')
					}
					event.WriteByte('\n')
					_, _ = tracker.Write(event.Bytes())
				}
				observation, complete = tracker.Finish()
				streaming = streaming || complete && observation.Status == "verified"
			} else {
				observation = evidence.Parse(f.Response, p.Candidate.Scope.Protocol)
				complete = observation.Terminal == "completed"
			}
			r.Passed = r.Passed && observation.Status == f.Status && observation.Terminal == f.Terminal && complete == f.Complete && reflect.DeepEqual(observation.Buckets, f.Buckets)
			positive = positive || f.Status == "verified"
			negative = negative || f.Status == "unsupported" || f.Status == "invalid" || f.Status == "missing" || f.Status == "ambiguous"
		}
		if !r.Passed {
			out.Failures++
		}
		out.Results = append(out.Results, r)
	}
	if !positive || !negative || !streaming {
		return out, fmt.Errorf("verified, negative and stream termination regression coverage required")
	}
	if out.Failures > 0 {
		out.Status = "replay_failed"
	}
	out.ReplayID = digest(struct {
		ProposalID, FixtureDigest string
		Results                   []Result
	}{out.ProposalID, out.FixtureDigest, out.Results})
	return out, nil
}

func sameJSON(a, b []byte) bool {
	decode := func(raw []byte) ([]byte, error) {
		var v any
		d := jsonx.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		return jsonx.Marshal(v)
	}
	aa, err := decode(a)
	if err != nil {
		return false
	}
	bb, err := decode(b)
	return err == nil && bytes.Equal(aa, bb)
}
