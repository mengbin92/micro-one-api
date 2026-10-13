package provider

import "testing"

func TestResolveModelPreservesExecutionMappingAtOwnerBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, model, mapping, upstream, want string
		configured                           []string
	}{
		{"JSON overrides explicit alias", "model", `{"model":"actual"}`, "registry", "actual", nil},
		{"wildcard mapping", "claude-sonnet-4[1M]", `{"*":"default","claude-*":"family","claude-sonnet-*":"sonnet"}`, "", "sonnet", nil},
		{"explicit alias", "model", "", "RegistryCase", "RegistryCase", nil},
		{"configured casing", "deepseek-v4", "", "", "DeepSeek-V4", []string{"*", "DeepSeek-V4"}},
		{"catch-all keeps requested model", "unlisted[1M]", "", "", "unlisted", []string{"*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveModel(tc.model, tc.mapping, tc.upstream, tc.configured); got != tc.want {
				t.Fatalf("resolved model=%q, want %q", got, tc.want)
			}
		})
	}
}
