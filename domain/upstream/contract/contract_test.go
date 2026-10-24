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

func TestTransformRulesRequireExplicitTypes(t *testing.T) {
	for _, rule := range []Rule{
		{ID: "rename", Kind: "rename", Path: "old_option", Target: "new_option", Evidence: "schema"},
		{ID: "default", Kind: "default", Path: "temperature", Value: jsonx.RawMessage(`0`), Evidence: "schema"},
	} {
		t.Run(rule.Kind, func(t *testing.T) {
			d := doc()
			d.Sets = []RuleSet{{Level: "model", Rules: []Rule{rule}}}
			_, err := Compile(d)
			require.Error(t, err, "a transformation without a known input type must never publish")
		})
	}
}

func TestDefaultNeverAcceptsAnExistingUnexpectedType(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "default", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`0`), Evidence: "schema"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	_, _, err = Apply(s, []byte(`{"temperature":"unexpected"}`))
	require.Error(t, err, "an existing wrong type must fail instead of passing through the default rule")
}

func TestTransformsValidateEveryConsumedTypeWithoutCoercion(t *testing.T) {
	values := map[string]string{"number": `0`, "string": `"private-input"`, "boolean": `false`, "object": `{"nested":"raw"}`, "array": `[0,null]`, "null": `null`}
	for expected, valid := range values {
		for _, kind := range []string{"rename", "default"} {
			t.Run(expected+"/"+kind, func(t *testing.T) {
				rule := Rule{ID: "typed", Kind: kind, Path: "option", Type: expected, Evidence: "schema"}
				if kind == "rename" {
					rule.Target = "new_option"
				} else {
					rule.Value = jsonx.RawMessage(valid)
				}
				d := doc()
				d.Sets = []RuleSet{{Level: "model", Rules: []Rule{rule}}}
				s, err := Compile(d)
				require.NoError(t, err)
				for actual, input := range values {
					out, _, err := Apply(s, []byte(`{"option":`+input+`,"extension":{"unseen":[null,true]}}`))
					if actual == expected {
						require.NoError(t, err)
						require.Contains(t, string(out), `"extension":{"unseen":[null,true]}`)
						continue
					}
					var mismatch *TypeMismatchError
					require.ErrorAs(t, err, &mismatch)
					require.Equal(t, expected, mismatch.Expected)
					require.Equal(t, actual, mismatch.Actual)
					require.Equal(t, "option", mismatch.Path)
					require.Equal(t, s.Revision, mismatch.Revision)
					require.Equal(t, "typed", mismatch.RuleID)
					require.NotContains(t, err.Error(), "private-input")
					require.Nil(t, out)
				}
				out, _, err := Apply(s, []byte(`{}`))
				require.NoError(t, err)
				if kind == "default" {
					require.JSONEq(t, `{"option":`+valid+`}`, string(out))
				}
			})
		}
	}
}

func TestTransformPublicationRejectsUnknownTypesAndInvalidDefaults(t *testing.T) {
	for _, kind := range []string{"rename", "default"} {
		for _, invalid := range []string{"", "integer", "any", "Number", "number|string"} {
			d := doc()
			rule := Rule{ID: "typed", Kind: kind, Path: "option", Type: invalid, Evidence: "schema"}
			if kind == "rename" {
				rule.Target = "new_option"
			} else {
				rule.Value = jsonx.RawMessage(`0`)
			}
			d.Sets = []RuleSet{{Level: "model", Rules: []Rule{rule}}}
			_, err := Compile(d)
			require.Error(t, err)
		}
	}
	for _, value := range []string{`"0"`, `null`, `true`, `[]`, `{}`, `1e999999999`} {
		d := doc()
		d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "typed", Kind: "default", Path: "option", Type: "number", Value: jsonx.RawMessage(value), Evidence: "schema"}}}}
		_, err := Compile(d)
		require.Error(t, err)
	}
	d := doc()
	d.AdapterBuild = "finite-contract-runtime-2026-10-14.1"
	_, err := Compile(d)
	require.Error(t, err, "the untyped runtime must not share the new contract build identity")
}

func TestRenameChecksTargetWhenSourceIsAbsentAndPreflightsAllRules(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{
		{ID: "first", Kind: "default", Path: "a_default", Type: "number", Value: jsonx.RawMessage(`0`), Evidence: "schema"},
		{ID: "rename", Kind: "rename", Path: "old_option", Target: "new_option", Type: "number", Evidence: "schema"},
	}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, body := range []string{`{"new_option":"0"}`, `{"old_option":0,"new_option":false}`} {
		out, trace, err := Apply(s, []byte(body))
		var mismatch *TypeMismatchError
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, "new_option", mismatch.Path)
		require.Nil(t, out)
		require.Empty(t, trace.Paths, "no earlier default may be applied before all types pass")
		require.Empty(t, trace.RuleIDs)
	}
}

func TestDeepSeekProofProfileRequiresInstalledExactScope(t *testing.T) {
	d := doc()
	d.Provider, d.Scope.Model = "deepseek", "deepseek-flash"
	d.ParserRevision, d.UsageRevision = DeepSeekProofParserID, DeepSeekProofUsageID
	_, err := Compile(d)
	require.NoError(t, err)
	for _, change := range []func(*Document){
		func(d *Document) { d.Provider = "other" },
		func(d *Document) { d.Scope.Model = "other" },
		func(d *Document) { d.Scope.Protocol = "responses" },
		func(d *Document) { d.ParserRevision = ParserID },
		func(d *Document) { d.UsageRevision = UsageID },
	} {
		invalid := d
		change(&invalid)
		_, err := Compile(invalid)
		require.Error(t, err, "profile versions and vendor/model/protocol cannot be mixed")
	}
}
func TestFiniteRulesKeepUnknownNullZeroAndExplicitOverrides(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "provider", Rules: []Rule{{ID: "base", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`1`), Evidence: "basis"}}}, {Level: "model", Rules: []Rule{{ID: "exact", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`0`), Replaces: "base", Evidence: "fixture"}, {ID: "rename", Kind: "rename", Path: "old_option", Target: "new_option", Type: "object", Evidence: "equivalence"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, value := range []string{"0", "0.0"} {
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
		{{ID: "a", Kind: "rename", Path: "a", Target: "b", Type: "number", Evidence: "x"}, {ID: "b", Kind: "rename", Path: "b", Target: "a", Type: "number", Evidence: "x"}},
		{{ID: "a", Kind: "rename", Path: "a", Target: "c", Type: "number", Evidence: "x"}, {ID: "b", Kind: "rename", Path: "b", Target: "c", Type: "number", Evidence: "x"}},
		{{ID: "a", Kind: "default", Path: "max_tokens", Type: "number", Value: jsonx.RawMessage(`999`), Evidence: "x"}},
		{{ID: "a", Kind: "drop", Path: "tools", Evidence: "x"}},
		{{ID: "a", Kind: "rename", Path: "a", Target: "url", Type: "number", Evidence: "x"}},
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
	d.Sets = []RuleSet{{Level: "provider", Rules: []Rule{{ID: "base", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`0`), Evidence: "x"}}}, {Level: "source", Rules: []Rule{{ID: "override", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`1`), Evidence: "x"}}}}
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
		{{ID: "a", Kind: "rename", Path: "a", Target: "options", Type: "number", Evidence: "x"}, {ID: "b", Kind: "rename", Path: "b", Target: "options.value", Type: "number", Evidence: "x"}},
		{{ID: "a", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`0`), Evidence: "x"}, {ID: "b", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`1`), Replaces: "a", Evidence: "x"}},
	} {
		d := doc()
		d.Sets = []RuleSet{{Level: "model", Rules: rules}}
		_, err := Compile(d)
		require.Error(t, err, "conflicting writes must fail at publication")
	}
}

func TestContractRejectsDuplicateRequestAndDefaultKeys(t *testing.T) {
	d := doc()
	d.Sets = []RuleSet{{Level: "model", Rules: []Rule{{ID: "rename", Kind: "rename", Path: "a", Target: "b", Type: "number", Evidence: "equivalence"}}}}
	s, err := Compile(d)
	require.NoError(t, err)
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":{"x":1,"x":2}}`} {
		_, _, err := Apply(s, []byte(raw))
		require.Error(t, err, "duplicate request values must not be silently selected")
	}
	d.Sets[0].Rules = []Rule{{ID: "default", Kind: "default", Path: "options", Type: "object", Value: jsonx.RawMessage(`{"x":1,"x":2}`), Evidence: "fixture"}}
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
