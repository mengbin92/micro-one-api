package contract

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"strings"
	"testing"
)

func doc() Document {
	return Document{Scope: Scope{Kind: "channel", ID: 1, Model: "vendor/Exact", Protocol: "openai_chat"}, Provider: "fixture", AdapterBuild: BuildID, ParserRevision: ParserID, UsageRevision: UsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "independent-matrix", BasisRevision: "provider-schema-v1"}
}
func TestFiniteRulesKeepUnknownNullZeroAndExplicitOverrides(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "provider", Rules: []Rule{{ID: "base", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`1`), Evidence: "basis"}}}, {Level: "model", Rules: []Rule{{ID: "exact", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`0`), Replaces: "base", Evidence: "fixture"}, {ID: "rename", Kind: "rename", Path: "old_option", Target: "new_option", Evidence: "equivalence"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, value := range []string{"null", "0"} {
		out, tag, err := Apply(s, []byte(`{"model":"vendor/Exact","temperature":`+value+`,"old_option":{"x":1},"unknown":{"preserve":true}}`))
		require.NoError(t, err)
		var m map[string]jsonx.RawMessage
		require.NoError(t, jsonx.Unmarshal(out, &m))
		require.Equal(t, value, string(m["temperature"]))
		require.NotContains(t, m, "old_option")
		require.Contains(t, m, "new_option")
		require.Contains(t, m, "unknown")
		require.Equal(t, []string{"old_option", "new_option"}, tag.Paths)
	}
	_, _, err = Apply(s, []byte(`{"old_option":1,"new_option":2}`))
	require.Error(t, err)
}
func TestConflictUnsafeRulesUninstalledBuildAndCaseNeverPublish(t *testing.T) {
	for _, rules := range [][]Rule{
		{{ID: "a", Kind: "rename", Path: "a", Target: "b", Evidence: "x"}, {ID: "b", Kind: "rename", Path: "b", Target: "a", Evidence: "x"}},
		{{ID: "a", Kind: "rename", Path: "a", Target: "c", Evidence: "x"}, {ID: "b", Kind: "rename", Path: "b", Target: "c", Evidence: "x"}},
		{{ID: "a", Kind: "default", Path: "max_tokens", Value: jsonx.RawMessage(`999`), Evidence: "x"}},
		{{ID: "a", Kind: "drop", Path: "tools", Evidence: "x"}},
		{{ID: "a", Kind: "rename", Path: "a", Target: "url", Evidence: "x"}},
	} {
		d := doc()
		d.Sets = []RuleSet{{Level: "model", Rules: rules}}
		_, err := Compile(d)
		require.Error(t, err)
	}
	d := doc()
	d.AdapterBuild = "uninstalled"
	_, err := Compile(d)
	require.Error(t, err)
	a, b := doc().Scope, doc().Scope
	b.Model = "vendor/exact"
	require.NotEqual(t, a.Key(), b.Key())
	d = doc()
	d.Sets = []RuleSet{{Level: "provider", Rules: []Rule{{ID: "base", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`0`), Evidence: "x"}}}, {Level: "source", Rules: []Rule{{ID: "override", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`1`), Evidence: "x"}}}}
	_, err = Compile(d)
	require.Error(t, err)
}
func TestRejectTypesAndBudgetBoundaryNeverClamp(t *testing.T) {
	d := doc()
	min, max := 0.0, 16.0
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "budget", Kind: "reject", Path: "max_tokens", Type: "number", Min: &min, Max: &max, Evidence: "limits"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, v := range []string{`17`, `null`, `"16"`} {
		_, _, err := Apply(s, []byte(`{"max_tokens":`+v+`}`))
		require.Error(t, err)
	}
	out, _, err := Apply(s, []byte(`{"max_tokens":0}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"max_tokens":0}`, string(out))
}

func TestContractHTTPHasNoNetworkCallForInvalidParameters(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "reject", Kind: "reject", Path: "tools", Evidence: "unsupported-tools-contract"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	ctx := WithSelection(context.Background(), Selection{Snapshots: []Snapshot{s}, Inbound: "openai_chat"})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://fixture.invalid/chat/completions", strings.NewReader(`{"tools":[]}`))
	require.NoError(t, err)
	_, _, err = PrepareHTTP(req, "openai_chat")
	require.Error(t, err)
}

func TestContractHTTPGeminiVersionMatchesURLPath(t *testing.T) {
	d := doc()
	d.Scope.Protocol = "gemini"
	d.Inbound = []string{"gemini"}
	d.APIVersion = "v1beta"
	snapshot, err := Compile(d)
	require.NoError(t, err)
	ctx := WithSelection(context.Background(), Selection{Snapshots: []Snapshot{snapshot}, Inbound: "gemini"})
	for _, prefix := range []string{"", "/google", "/proxy/models/google"} {
		for _, version := range []string{"v1beta", "v1"} {
			t.Run(prefix+"/"+version, func(t *testing.T) {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid"+prefix+"/"+version+"/models/vendor-exact:generateContent", strings.NewReader(`{"contents":[]}`))
				require.NoError(t, err)
				_, trace, err := PrepareHTTP(req, "gemini")
				if version == d.APIVersion {
					require.NoError(t, err, "the Gemini provider puts the API version after any configured base path")
					require.Equal(t, snapshot.Revision, trace.Revision)
				} else {
					require.ErrorContains(t, err, "API version does not match")
				}
			})
		}
	}
}

func TestContractRejectsOverlappingRenameDestinationsAndSameLevelOverrides(t *testing.T) {
	for _, rules := range [][]Rule{
		{{ID: "a", Kind: "rename", Path: "a", Target: "options", Evidence: "x"}, {ID: "b", Kind: "rename", Path: "b", Target: "options.value", Evidence: "x"}},
		{{ID: "a", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`0`), Evidence: "x"}, {ID: "b", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`1`), Replaces: "a", Evidence: "x"}},
	} {
		d := doc()
		d.Sets = []RuleSet{{Level: "model", Rules: rules}}
		_, err := Compile(d)
		require.Error(t, err, "conflicting writes must fail at publication")
	}
}

func TestContractRejectsDuplicateRequestAndDefaultKeys(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "rename", Kind: "rename", Path: "a", Target: "b", Evidence: "equivalence"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":{"x":1,"x":2}}`} {
		_, _, err := Apply(s, []byte(raw))
		require.Error(t, err, "duplicate request values must not be silently selected")
	}
	d.Sets[0].Rules = []Rule{{ID: "default", Kind: "default", Path: "options", Value: jsonx.RawMessage(`{"x":1,"x":2}`), Evidence: "fixture"}}
	_, err = Compile(d)
	require.Error(t, err, "defaults must have one unambiguous value")
}

func TestContractNumberRangeDoesNotRoundLargeIntegers(t *testing.T) {
	d := doc()
	max := float64(9007199254740992)
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "range", Kind: "reject", Path: "value", Type: "number", Max: &max, Evidence: "limits"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	_, _, err = Apply(s, []byte(`{"value":9007199254740993}`))
	require.Error(t, err, "one unit above the range is invalid even beyond float precision")
	_, _, err = Apply(s, []byte(`{"value":9007199254740992}`))
	require.NoError(t, err)
}

func TestContractNumericRulesRejectUnboundedDecimalExponents(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "number", Kind: "reject", Path: "value", Type: "number", Evidence: "finite-numeric-domain"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, value := range []string{"1e500", "1e-500", "1e1000000000", "1e-1000000000", "0e9999999999999999999999999"} {
		_, _, err := Apply(s, []byte(`{"value":`+value+`}`))
		require.Error(t, err, "unbounded exponents must be rejected before arbitrary precision allocation")
	}
	for _, value := range []string{"1e308", "1e-308"} {
		_, _, err := Apply(s, []byte(`{"value":`+value+`}`))
		require.NoError(t, err)
	}
}
