// Package wildcard provides case-insensitive glob matching for model names.
//
// It supports the shell-style wildcards used by the model-management layer
// (docs/model-management-design.md §9.3 #4):
//
//   - "*" matches any sequence of characters (including empty)
//   - "?" matches any single character
//   - "claude-*" matches "claude-sonnet-4", "claude-3-5-sonnet", etc.
//   - "*" matches everything (the catch-all used when RestrictModels=false
//     or as a model_mapping catch-all key)
//
// Matching is case-insensitive to match the rest of the routing stack
// (abilities are stored and compared LOWER(model) = LOWER(?)).
package wildcard

import "strings"

// IsPattern reports whether s contains a wildcard metacharacter ("*" or "?").
// A plain model name like "gpt-4o" is not a pattern; "claude-*" is.
func IsPattern(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// Match reports whether the glob pattern matches name.
// The match is case-insensitive. An empty pattern matches only an empty name.
// "*" matches any (possibly empty) sequence. "?" matches exactly one character.
func Match(pattern, name string) bool {
	return match(strings.ToLower(pattern), strings.ToLower(name))
}

// match implements the wildcard glob: "*" = any sequence, "?" = single char.
// It is a recursive descent over the pattern.
func match(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			// Collapse consecutive '*' — "**" is equivalent to "*".
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true // trailing "*" matches the rest
			}
			// Try to match the remaining pattern at every suffix of name.
			for i := 0; i <= len(name); i++ {
				if match(pattern, name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(name) == 0 {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		}
	}
	return len(name) == 0
}

// FirstMatch is retained for backwards compatibility but is deprecated. It
// returns the first key in keys (in iteration order) whose glob pattern
// matches name. It does NOT apply the most-specific-wins rule and is
// therefore non-deterministic when several specific patterns match the same
// name. Production code should use Specificity + Match to pick the most
// specific match (see internal/biz/model_mapping.go Resolve). Removed from
// active call sites as part of docs/design/model-management-code-review.md..
//
// Deprecated: use a most-specific-wins selection instead.
func FirstMatch(keys []string, name string) (string, bool) {
	for _, k := range keys {
		if Match(k, name) {
			return k, true
		}
	}
	return "", false
}

// Specificity returns a non-negative score measuring how specific a glob
// pattern is: the count of non-wildcard characters. "claude-sonnet-*" (15)
// is more specific than "claude-*" (7), which is more specific than "*" (0).
// Ties are broken by the full pattern length so "claude-*-x" (9 non-wild,
// len 10) ranks above "claude-*" (7 non-wild, len 8). Used to make wildcard
// matching deterministic: when several specific patterns match the same
// name, the most specific one wins, so two requests for the same model
// resolve to the same upstream instead of being randomised by Go map
// iteration order. See docs/model-management-design.md §11.1.
func Specificity(pattern string) int {
	nonWildcard := 0
	for _, c := range pattern {
		if c != '*' && c != '?' {
			nonWildcard++
		}
	}
	return nonWildcard
}
