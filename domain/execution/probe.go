package execution

import (
	"crypto/sha256"
	"fmt"
	"micro-one-api/pkg/jsonx"
	"strings"
)

// ProbeTarget is operator configuration, never a public request field.
type ProbeTarget struct {
	Capability             string   `json:"capability,omitempty"`
	ManualModelIDs         []string `json:"manual_model_ids,omitempty"`
	ID                     string   `json:"id"`
	SourceKind             string   `json:"source_kind"`
	SourceID               int64    `json:"source_id"`
	Model                  string   `json:"model"`
	Protocol               string   `json:"protocol"`
	Stream                 bool     `json:"stream"`
	Inventory              bool     `json:"inventory"`
	CallsPerDay            int64    `json:"calls_per_day"`
	InputTokensPerDay      int64    `json:"input_tokens_per_day"`
	OutputTokensPerDay     int64    `json:"output_tokens_per_day"`
	MaxOutputTokens        int64    `json:"max_output_tokens"`
	CostUpperBound         *int64   `json:"cost_upper_bound,omitempty"`
	CostPerDay             int64    `json:"cost_per_day"`
	UnknownCostCallsPerDay int64    `json:"unknown_cost_calls_per_day"`
}

func DecodeProbeTargets(raw string) ([]ProbeTarget, error) {
	var targets []ProbeTarget
	if raw == "" {
		return targets, nil
	}
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("probe configuration exceeds limit")
	}
	if err := jsonx.Unmarshal([]byte(raw), &targets); err != nil {
		return nil, err
	}
	if len(targets) > 64 {
		return nil, fmt.Errorf("too many probe targets")
	}
	seen := map[string]bool{}
	budgets := map[string]ProbeTarget{}
	for _, t := range targets {
		if t.ID == "" || len(t.ID) > 64 || strings.ContainsAny(t.ID, "/\r\n") || seen[t.ID] || t.SourceID <= 0 || (t.SourceKind != "channel" && t.SourceKind != "subscription") || t.Model == "" && !t.Inventory || len(t.Model) > 255 || t.CallsPerDay < 1 || t.CallsPerDay > 10000 || t.InputTokensPerDay < 128 || t.OutputTokensPerDay < t.MaxOutputTokens || t.MaxOutputTokens < 1 || t.MaxOutputTokens > 16 || t.CostPerDay < 0 || t.UnknownCostCallsPerDay < 0 {
			return nil, fmt.Errorf("invalid probe target or budget")
		}
		if t.Capability != "" && t.Capability != "text" && t.Capability != "tools" {
			return nil, fmt.Errorf("unsupported probe capability")
		}
		if len(t.ManualModelIDs) > 10000 || len(t.ManualModelIDs) > 0 && !t.Inventory {
			return nil, fmt.Errorf("manual models require inventory job")
		}
		switch t.Protocol {
		case "openai_chat", "responses", "anthropic_messages", "gemini":
		default:
			return nil, fmt.Errorf("unsupported probe protocol")
		}
		if t.CostUpperBound == nil && t.UnknownCostCallsPerDay < 1 || t.CostUpperBound != nil && (*t.CostUpperBound < 0 || *t.CostUpperBound > t.CostPerDay) {
			return nil, fmt.Errorf("probe requires a cost bound or explicit unknown-exposure budget")
		}
		source := fmt.Sprintf("%s:%d", t.SourceKind, t.SourceID)
		if old, ok := budgets[source]; ok && (old.CallsPerDay != t.CallsPerDay || old.InputTokensPerDay != t.InputTokensPerDay || old.OutputTokensPerDay != t.OutputTokensPerDay || old.CostPerDay != t.CostPerDay || old.UnknownCostCallsPerDay != t.UnknownCostCallsPerDay) {
			return nil, fmt.Errorf("source budgets must agree across probe jobs")
		}
		budgets[source] = t
		seen[t.ID] = true
	}
	return targets, nil
}

type ProbeResult struct {
	RealTraffic        jsonx.RawMessage `json:"real_traffic,omitempty"`
	AttemptID          string           `json:"attempt_id"`
	TargetID           string           `json:"target_id"`
	Inventory          []string         `json:"inventory,omitempty"`
	RecordJSON         jsonx.RawMessage `json:"record_json,omitempty"`
	Outcome            string           `json:"outcome"`
	ValidContractProbe bool             `json:"valid_contract_probe"`
	ObservedAt         int64            `json:"observed_at"`
}

func (t ProbeTarget) Digest() string {
	b, _ := jsonx.Marshal(t)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
