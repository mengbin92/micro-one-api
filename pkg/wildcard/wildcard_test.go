package wildcard

import "testing"

func TestIsPattern(t *testing.T) {
	cases := map[string]bool{
		"gpt-4o":           false,
		"claude-*":         true,
		"claude-?-sonnet":  true,
		"*":                true,
		"":                 false,
		"no-meta-here-123": false,
	}
	for in, want := range cases {
		if got := IsPattern(in); got != want {
			t.Errorf("IsPattern(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"gpt-4o", "gpt-4o", true},
		{"gpt-4o", "GPT-4O", true}, // case-insensitive
		{"gpt-4o", "gpt-4o-mini", false},
		{"claude-*", "claude-sonnet-4", true},
		{"claude-*", "claude-3-5-sonnet-20241022", true},
		{"claude-*", "gpt-4o", false},
		{"claude-?-sonnet", "claude-3-sonnet", true},
		{"claude-?-sonnet", "claude-sonnet", false}, // ? = exactly one char
		{"*", "anything", true},
		{"*", "", true},
		{"*", "gpt-4o", true},
		{"", "", true},
		{"", "gpt-4o", false},
		{"gpt-*o", "gpt-4o", true},
		{"gpt-*o", "gpt-4o-mini", false},
		{"**", "gpt-4o", true}, // collapse consecutive *
		{"claude-*-4", "claude-sonnet-4", true},
		{"*-mini", "gpt-4o-mini", true},
		{"?-pt-4o", "g-pt-4o", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(pattern=%q, name=%q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestFirstMatch(t *testing.T) {
	keys := []string{"claude-*", "*"}
	if k, ok := FirstMatch(keys, "claude-sonnet-4"); !ok || k != "claude-*" {
		t.Errorf("expected claude-*, got %q/%v", k, ok)
	}
	if k, ok := FirstMatch(keys, "gpt-4o"); !ok || k != "*" {
		t.Errorf("expected * catch-all, got %q/%v", k, ok)
	}
	// "*" matches the empty name too (any sequence, including empty).
	if k, ok := FirstMatch([]string{"*"}, ""); !ok || k != "*" {
		t.Errorf("Match(\"*\") for empty name = %q/%v, want *", k, ok)
	}
	// No keys match a name with no wildcard and no exact match.
	if _, ok := FirstMatch([]string{"claude-*"}, "gpt-4o"); ok {
		t.Error("gpt-4o should not match claude-*")
	}
}
