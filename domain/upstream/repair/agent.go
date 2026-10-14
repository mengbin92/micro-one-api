package repair

import (
	"context"
	"crypto/sha256"
	"fmt"
	"micro-one-api/pkg/jsonx"
	"reflect"
	"strings"
)

// Context is a reviewed, redacted snapshot, not a filesystem or tool grant.
// The generator sees only pinned schema/code and a fixed incident template.
type AgentContext struct {
	Template        Proposal          `json:"template"`
	OfficialSchema  string            `json:"official_schema"`
	Code            map[string]string `json:"code"`
	Usage           jsonx.RawMessage  `json:"native_usage"`
	Model           string            `json:"model"`
	MaxOutputTokens int               `json:"max_output_tokens"`
}
type Generation struct {
	Proposal     Proposal `json:"proposal"`
	RequestID    string   `json:"provider_request_id"`
	ResponseID   string   `json:"provider_response_id"`
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
	CostBasis    string   `json:"cost_basis"`
	Amount       *string  `json:"amount"`
	Purpose      string   `json:"purpose"`
}
type Agent interface {
	Generate(context.Context, AgentContext) (Generation, error)
}

func (c AgentContext) Validate() error {
	if c.Model == "" || len(c.Model) > 128 || strings.ContainsAny(c.Model, "\r\n") || c.MaxOutputTokens < 256 || c.MaxOutputTokens > 8192 || len(c.OfficialSchema) == 0 || len(c.OfficialSchema) > 32<<10 || len(c.Code) > 16 || len(c.Usage) > 32<<10 {
		return fmt.Errorf("fixed model, redacted context and output budget required")
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(c.OfficialSchema))) != c.Template.BasisDigest || c.Template.Classification != "protocol_drift" || !c.Template.ContractProbeFailed || c.Template.Candidate.Scope.Validate() != nil || c.Template.Candidate.Repair != nil {
		return fmt.Errorf("pinned official schema and unsigned protocol incident required")
	}
	total := len(c.OfficialSchema) + len(c.Usage)
	for path, body := range c.Code {
		if len(body) > 16<<10 || strings.Contains(path, "..") || !strings.HasSuffix(path, ".go") || !(strings.HasPrefix(path, "internal/adaptor/") || strings.HasPrefix(path, "internal/apicompat/") || strings.HasPrefix(path, "internal/server/usage/") || strings.HasPrefix(path, "domain/upstream/evidence/")) {
			return fmt.Errorf("agent code context exceeds candidate workspace")
		}
		total += len(body)
	}
	if total > 64<<10 {
		return fmt.Errorf("agent input byte budget exceeded")
	}
	encoded, err := jsonx.Marshal(c)
	if err != nil || len(encoded) > 128<<10 {
		return fmt.Errorf("full agent context budget exceeded")
	}
	// Token-only usage evidence prevents prompts, response text, credentials
	// and upstream error prose from entering the generation context.
	if jsonx.ValidateUnique(c.Usage) != nil {
		return fmt.Errorf("valid redacted usage required")
	}
	var usage any
	d := jsonx.NewDecoder(strings.NewReader(string(c.Usage)))
	d.UseNumber()
	if d.Decode(&usage) != nil {
		return fmt.Errorf("invalid usage evidence")
	}
	var safe func(any) bool
	safe = func(v any) bool {
		switch x := v.(type) {
		case nil, bool, jsonx.Number:
			return true
		case map[string]any:
			for k, v := range x {
				if len(k) > 128 || strings.ContainsAny(k, "\r\n") || !safe(v) {
					return false
				}
			}
			return true
		case []any:
			for _, v := range x {
				if !safe(v) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !safe(usage) {
		return fmt.Errorf("usage strings and unredacted content are forbidden")
	}
	return nil
}

func BindGeneration(c AgentContext, generated Proposal) (Proposal, error) {
	if generated.Candidate.Scope != c.Template.Candidate.Scope || generated.Candidate.Provider != c.Template.Candidate.Provider || generated.Candidate.Endpoint != c.Template.Candidate.Endpoint || generated.Candidate.APIVersion != c.Template.Candidate.APIVersion || !reflect.DeepEqual(generated.Candidate.Inbound, c.Template.Candidate.Inbound) || generated.Candidate.Repair != nil {
		return Proposal{}, fmt.Errorf("agent altered protected candidate scope")
	}
	// Incident, source, basis and expected revisions come from operator facts,
	// never from the model's ability to invent a more convenient approval.
	p := c.Template
	p.Candidate = generated.Candidate
	p.Candidate.BasisRevision = c.Template.Candidate.BasisRevision
	p.Candidate.FixtureEvidence = c.Template.Candidate.FixtureEvidence
	p.Patch = generated.Patch
	return p, nil
}
