// Package contract is the finite, code-installed upstream request contract.
// It cannot change credentials, destinations, billing units or product budgets.
package contract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"micro-one-api/pkg/jsonx"
)

var ErrQuarantined = errors.New("protocol incident is quarantined")

const BuildID = "finite-contract-runtime-2026-10-14.1"
const ParserID = "native-shadow-2026-10-13.1"
const UsageID = "native-static-2026-10-13.1"

type Scope struct {
	Kind     string `json:"source_kind"`
	ID       int64  `json:"source_id"`
	Model    string `json:"upstream_model_id"`
	Protocol string `json:"protocol"`
}

func (s Scope) Key() string { b, _ := jsonx.Marshal(s); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func (s Scope) Validate() error {
	if (s.Kind != "channel" && s.Kind != "subscription") || s.ID <= 0 || s.Model == "" || len(s.Model) > 255 || strings.ContainsAny(s.Model, "*?\x00\r\n") {
		return fmt.Errorf("exact source/model scope required")
	}
	switch s.Protocol {
	case "openai_chat", "responses", "anthropic_messages", "gemini":
		return nil
	}
	return fmt.Errorf("unsupported protocol")
}

type Rule struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	Path     string           `json:"path"`
	Target   string           `json:"target,omitempty"`
	Value    jsonx.RawMessage `json:"value,omitempty"`
	Type     string           `json:"type,omitempty"`
	Min      *float64         `json:"min,omitempty"`
	Max      *float64         `json:"max,omitempty"`
	Replaces string           `json:"replaces,omitempty"`
	Evidence string           `json:"evidence_id"`
}
type RuleSet struct {
	Level string `json:"level"`
	Rules []Rule `json:"rules"`
}
type Document struct {
	Repair          *RepairReview `json:"repair_review,omitempty"`
	Scope           Scope         `json:"scope"`
	Provider        string        `json:"provider"`
	AdapterBuild    string        `json:"adapter_build"`
	ParserRevision  string        `json:"parser_revision"`
	UsageRevision   string        `json:"usage_revision"`
	APIVersion      string        `json:"api_version,omitempty"`
	Endpoint        string        `json:"endpoint,omitempty"`
	Inbound         []string      `json:"inbound_protocols"`
	Sets            []RuleSet     `json:"rule_sets"`
	FixtureEvidence string        `json:"fixture_evidence_id"`
	BasisRevision   string        `json:"basis_revision"`
}
type Snapshot struct {
	Document         Document `json:"document"`
	Revision         string   `json:"revision"`
	BindingRevision  int64    `json:"binding_revision"`
	IncidentRevision int64    `json:"incident_revision"`
	Rules            []Rule   `json:"rules"`
}
type Trace struct {
	Scope            Scope    `json:"scope"`
	Revision         string   `json:"revision"`
	Build            string   `json:"build"`
	BindingRevision  int64    `json:"binding_revision"`
	IncidentRevision int64    `json:"incident_revision"`
	RuleIDs          []string `json:"rule_ids,omitempty"`
	Paths            []string `json:"paths,omitempty"`
}

func (d Document) Revision() string {
	b, _ := jsonx.Marshal(d)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func pathParts(path string) ([]string, error) {
	p := strings.Split(path, ".")
	if len(p) > 8 || len(path) > 128 {
		return nil, fmt.Errorf("invalid field path")
	}
	for _, v := range p {
		if v == "" {
			return nil, fmt.Errorf("invalid field path")
		}
		for _, r := range v {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				return nil, fmt.Errorf("field paths must be exact object keys")
			}
		}
	}
	return p, nil
}
func protected(path string) bool {
	root := strings.Split(path, ".")[0]
	switch root {
	case "model", "stream", "messages", "input", "contents", "tools", "tool_choice", "response_format", "thinking", "cache_control", "max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig", "metadata", "authorization", "api_key", "url", "base_url", "user", "budget":
		return true
	}
	return false
}
func Compile(d Document) (Snapshot, error) {
	out := Snapshot{Document: d, Revision: d.Revision()}
	if err := d.Scope.Validate(); err != nil {
		return out, err
	}
	if d.AdapterBuild != BuildID || d.ParserRevision != ParserID || d.UsageRevision != UsageID || d.FixtureEvidence == "" || d.BasisRevision == "" || d.Provider == "" || len(d.Sets) > 3 || len(d.Inbound) == 0 {
		return out, fmt.Errorf("installed build/parser and independent fixture/basis required")
	}
	if len(d.Endpoint) > 256 || strings.ContainsAny(d.Endpoint, "?#\r\n") || d.Endpoint != "" && (!strings.HasPrefix(d.Endpoint, "/") || strings.HasPrefix(d.Endpoint, "//")) {
		return out, fmt.Errorf("endpoint is a relative validation constraint only")
	}
	inbound := map[string]bool{}
	for _, v := range d.Inbound {
		switch v {
		case "openai_chat", "responses", "anthropic_messages", "gemini":
		default:
			return out, fmt.Errorf("unsupported inbound protocol")
		}
		if inbound[v] {
			return out, fmt.Errorf("duplicate inbound protocol")
		}
		inbound[v] = true
	}
	levels := map[string]int{"provider": 0, "source": 1, "model": 2}
	sets := append([]RuleSet(nil), d.Sets...)
	sort.Slice(sets, func(i, j int) bool { return levels[sets[i].Level] < levels[sets[j].Level] })
	usedLevels, ids := map[string]bool{}, map[string]bool{}
	byPath := map[string]Rule{}
	for _, set := range sets {
		if _, ok := levels[set.Level]; !ok || usedLevels[set.Level] {
			return out, fmt.Errorf("invalid/duplicate rule precedence")
		}
		usedLevels[set.Level] = true
		levelPaths := map[string]bool{}
		for _, r := range set.Rules {
			if len(ids) >= 64 || r.ID == "" || len(r.ID) > 128 || ids[r.ID] || r.Evidence == "" {
				return out, fmt.Errorf("bounded, unique, evidenced rules required")
			}
			ids[r.ID] = true
			if _, err := pathParts(r.Path); err != nil {
				return out, err
			}
			if levelPaths[r.Path] {
				return out, fmt.Errorf("same precedence field conflict: %s", r.Path)
			}
			levelPaths[r.Path] = true
			if r.Kind != "reject" && protected(r.Path) {
				return out, fmt.Errorf("protected request semantics: %s", r.Path)
			}
			old, exists := byPath[r.Path]
			if exists && r.Replaces != old.ID || !exists && r.Replaces != "" {
				return out, fmt.Errorf("explicit predecessor rule required: %s", r.Path)
			}
			switch r.Kind {
			case "rename":
				if _, err := pathParts(r.Target); err != nil {
					return out, err
				}
				if protected(r.Target) || r.Target == r.Path || len(r.Value) > 0 || r.Type != "" || r.Min != nil || r.Max != nil {
					return out, fmt.Errorf("invalid rename")
				}
			case "default":
				if len(r.Value) == 0 || len(r.Value) > 1024 || jsonx.ValidateUnique(r.Value) != nil || r.Target != "" || r.Min != nil || r.Max != nil || r.Type != "" {
					return out, fmt.Errorf("invalid default")
				}
			case "reject":
				if r.Min != nil && (math.IsNaN(*r.Min) || math.IsInf(*r.Min, 0)) || r.Max != nil && (math.IsNaN(*r.Max) || math.IsInf(*r.Max, 0)) {
					return out, fmt.Errorf("nonfinite range")
				}
				if r.Target != "" || len(r.Value) > 0 {
					return out, fmt.Errorf("invalid reject")
				}
				switch r.Type {
				case "", "number", "string", "boolean", "object", "array":
				default:
					return out, fmt.Errorf("unsupported parameter type")
				}
				if (r.Min != nil || r.Max != nil) && r.Type != "number" || r.Min != nil && r.Max != nil && *r.Min > *r.Max {
					return out, fmt.Errorf("invalid parameter range")
				}
			default:
				return out, fmt.Errorf("only rename/default/reject are installed")
			}
			byPath[r.Path] = r
		}
	}
	destinations := map[string]bool{}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		r := byPath[p]
		if r.Kind == "rename" {
			if destinations[r.Target] {
				return out, fmt.Errorf("multiple target writers")
			}
			for target := range destinations {
				if strings.HasPrefix(target, r.Target+".") || strings.HasPrefix(r.Target, target+".") {
					return out, fmt.Errorf("overlapping target writers")
				}
			}
			destinations[r.Target] = true
			if _, ok := byPath[r.Target]; ok {
				return out, fmt.Errorf("rename chains/cycles are forbidden")
			}
			for q := range byPath {
				if strings.HasPrefix(q, r.Target+".") || strings.HasPrefix(r.Target, q+".") {
					return out, fmt.Errorf("overlapping target paths")
				}
			}
		}
		for _, q := range paths {
			if p != q && strings.HasPrefix(p, q+".") {
				return out, fmt.Errorf("overlapping field rules")
			}
		}
		out.Rules = append(out.Rules, r)
	}
	return out, nil
}
func objectAt(obj map[string]jsonx.RawMessage, parts []string) (map[string]jsonx.RawMessage, bool, error) {
	if len(parts) == 1 {
		return obj, true, nil
	}
	raw, ok := obj[parts[0]]
	if !ok {
		return nil, false, nil
	}
	var child map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &child) != nil || child == nil {
		return nil, false, fmt.Errorf("parent is not an object")
	}
	return objectAt(child, parts[1:])
}
func get(obj map[string]jsonx.RawMessage, path string) (jsonx.RawMessage, bool, error) {
	p, _ := pathParts(path)
	parent, ok, err := objectAt(obj, p)
	if !ok || err != nil {
		return nil, false, err
	}
	v, found := parent[p[len(p)-1]]
	return v, found, nil
}
func write(obj map[string]jsonx.RawMessage, parts []string, value jsonx.RawMessage, remove bool) error {
	if len(parts) == 1 {
		if remove {
			delete(obj, parts[0])
		} else {
			obj[parts[0]] = value
		}
		return nil
	}
	raw, ok := obj[parts[0]]
	if !ok {
		return fmt.Errorf("parent missing: %s", parts[0])
	}
	var child map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &child) != nil || child == nil {
		return fmt.Errorf("parent is not an object")
	}
	if err := write(child, parts[1:], value, remove); err != nil {
		return err
	}
	b, err := jsonx.Marshal(child)
	obj[parts[0]] = b
	return err
}

func boundedNumber(n jsonx.Number) (*big.Rat, bool) {
	text := n.String()
	if len(text) > 1024 {
		return nil, false
	}
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.ParseInt(text[index+1:], 10, 64)
		// The installed contract has finite float64 range bounds. Bound
		// exponent work before big.Rat constructs a power of ten, including
		// underflow values that strconv.ParseFloat would silently round to 0.
		if err != nil || exponent < -324 || exponent > 324 {
			return nil, false
		}
	}
	value, err := n.Float64()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, false
	}
	return new(big.Rat).SetString(text)
}

func Apply(s Snapshot, body []byte) ([]byte, Trace, error) {
	trace := Trace{Scope: s.Document.Scope, Revision: s.Revision, Build: s.Document.AdapterBuild, BindingRevision: s.BindingRevision, IncidentRevision: s.IncidentRevision}
	if len(body) > 4<<20 {
		return nil, trace, fmt.Errorf("contract request size exceeded")
	}
	if jsonx.ValidateUnique(body) != nil {
		return nil, trace, fmt.Errorf("contract requires JSON with unique object keys")
	}
	var obj map[string]jsonx.RawMessage
	if jsonx.Unmarshal(body, &obj) != nil || obj == nil {
		return nil, trace, fmt.Errorf("contract requires a JSON object")
	}
	compiled, err := Compile(s.Document)
	if err != nil || s.Revision != compiled.Revision {
		return nil, trace, fmt.Errorf("snapshot integrity/build mismatch")
	}
	for _, r := range compiled.Rules {
		value, present, err := get(obj, r.Path)
		if err != nil {
			return nil, trace, err
		}
		if !present && r.Kind != "default" {
			continue
		}
		changed := false
		switch r.Kind {
		case "rename":
			other, found, err := get(obj, r.Target)
			if err != nil {
				return nil, trace, err
			}
			if found {
				var a, b any
				da := jsonx.NewDecoder(bytes.NewReader(value))
				da.UseNumber()
				db := jsonx.NewDecoder(bytes.NewReader(other))
				db.UseNumber()
				if da.Decode(&a) != nil || db.Decode(&b) != nil {
					return nil, trace, fmt.Errorf("invalid rename values")
				}
				aa, _ := jsonx.Marshal(a)
				bb, _ := jsonx.Marshal(b)
				if !bytes.Equal(aa, bb) {
					return nil, trace, fmt.Errorf("conflicting parameter %s", r.Target)
				}
			} else {
				parts, _ := pathParts(r.Target)
				if err := write(obj, parts, value, false); err != nil {
					return nil, trace, err
				}
			}
			parts, _ := pathParts(r.Path)
			_ = write(obj, parts, nil, true)
			changed = true
		case "default":
			if !present {
				parts, _ := pathParts(r.Path)
				if err := write(obj, parts, r.Value, false); err != nil {
					return nil, trace, err
				}
				changed = true
			}
		case "reject":
			var v any
			d := jsonx.NewDecoder(bytes.NewReader(value))
			d.UseNumber()
			if d.Decode(&v) != nil {
				return nil, trace, fmt.Errorf("invalid field %s", r.Path)
			}
			valid := false
			switch r.Type {
			case "":
				return nil, trace, fmt.Errorf("unsupported field %s", r.Path)
			case "number":
				n, ok := v.(jsonx.Number)
				if ok {
					// Compare the original decimal value before float64 rounding;
					// otherwise an integer immediately beyond a limit can pass.
					value, parsed := boundedNumber(n)
					valid = parsed
					for _, bound := range []struct {
						limit *float64
						min   bool
					}{{r.Min, true}, {r.Max, false}} {
						if !valid || bound.limit == nil {
							continue
						}
						limit, _ := new(big.Rat).SetString(strconv.FormatFloat(*bound.limit, 'g', -1, 64))
						comparison := value.Cmp(limit)
						valid = (bound.min && comparison >= 0) || (!bound.min && comparison <= 0)
					}
				}
			case "string":
				_, valid = v.(string)
			case "boolean":
				_, valid = v.(bool)
			case "object":
				_, valid = v.(map[string]any)
			case "array":
				_, valid = v.([]any)
			}
			if !valid {
				return nil, trace, fmt.Errorf("unsupported type/range for %s", r.Path)
			}
		}
		trace.RuleIDs = append(trace.RuleIDs, r.ID)
		if changed {
			trace.Paths = append(trace.Paths, r.Path)
			if r.Kind == "rename" {
				trace.Paths = append(trace.Paths, r.Target)
			}
		}
	}
	b, err := jsonx.Marshal(obj)
	return b, trace, err
}

type contextKey struct{}
type Selection struct {
	Snapshots []Snapshot
	Inbound   string
	Blocked   []string
}

func WithSelection(ctx context.Context, s Selection) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func Prepare(ctx context.Context, protocol string, body []byte) ([]byte, *Trace, error) {
	s, ok := ctx.Value(contextKey{}).(Selection)
	if !ok {
		return body, nil, nil
	}
	for _, p := range s.Blocked {
		if p == protocol {
			return nil, nil, ErrQuarantined
		}
	}
	for _, snap := range s.Snapshots {
		if snap.Document.Scope.Protocol != protocol {
			continue
		}
		if s.Inbound != "" {
			allowed := false
			for _, p := range snap.Document.Inbound {
				allowed = allowed || p == s.Inbound
			}
			if !allowed {
				return nil, nil, fmt.Errorf("conversion is not verified for this inbound protocol")
			}
		}
		out, trace, err := Apply(snap, body)
		return out, &trace, err
	}
	return body, nil, nil
}
func PrepareHTTP(req *http.Request, protocol string) (*http.Request, *Trace, error) {
	s, ok := req.Context().Value(contextKey{}).(Selection)
	if !ok {
		return req, nil, nil
	}
	has := false
	for _, p := range s.Blocked {
		has = has || p == protocol
	}
	for _, v := range s.Snapshots {
		has = has || v.Document.Scope.Protocol == protocol
	}
	if !has {
		return req, nil, nil
	}
	if req.Body == nil {
		return nil, nil, fmt.Errorf("contract body absent")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, (4<<20)+1))
	_ = req.Body.Close()
	if err != nil {
		return nil, nil, err
	}
	out, trace, err := Prepare(req.Context(), protocol, body)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range s.Snapshots {
		if v.Document.Scope.Protocol == protocol && v.Document.Endpoint != "" && req.URL.Path != v.Document.Endpoint {
			return nil, nil, fmt.Errorf("endpoint does not match frozen contract")
		}
		if v.Document.Scope.Protocol == protocol && v.Document.APIVersion != "" {
			actual := req.Header.Get("Anthropic-Version")
			if actual == "" {
				actual = req.URL.Query().Get("api-version")
			}
			if actual != v.Document.APIVersion {
				return nil, nil, fmt.Errorf("API version does not match frozen contract")
			}
		}
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(out))
	clone.ContentLength = int64(len(out))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	return clone, trace, nil
}

type Rejection struct{ Err error }

func (e *Rejection) Error() string        { return e.Err.Error() }
func (e *Rejection) Unwrap() error        { return e.Err }
func (e *Rejection) BeforeUpstream() bool { return true }

type inboundKey struct{}

func WithInbound(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, inboundKey{}, p)
}
func Inbound(ctx context.Context) string { p, _ := ctx.Value(inboundKey{}).(string); return p }
func Snapshots(ctx context.Context) []Snapshot {
	s, _ := ctx.Value(contextKey{}).(Selection)
	return s.Snapshots
}

type ProbeLease struct {
	ID             string
	Snapshot       Snapshot
	SourceRevision string
}
