// Package routing owns the shared vocabulary for access to upstream resources.
// A routing group is a string key, independent of subscription quota policies.
package routing

import "strings"

const DefaultGroup = "default"

// Groups parses the legacy CSV membership field on channels and upstream
// accounts. Keep case and order, trim separators, and remove duplicate members.
// An empty membership grants access to no group; it must not imply default.
func Groups(membership string) []string {
	var groups []string
	seen := make(map[string]bool)
	for _, group := range strings.Split(membership, ",") {
		group = strings.TrimSpace(group)
		if group != "" && !seen[group] {
			groups = append(groups, group)
			seen[group] = true
		}
	}
	return groups
}

// ContainsGroup checks an exact, case-sensitive routing group membership.
// The request group is a single key, never a CSV list or a wildcard pattern.
func ContainsGroup(membership, group string) bool {
	if group == "" || group != strings.TrimSpace(group) || strings.Contains(group, ",") {
		return false
	}
	for _, member := range Groups(membership) {
		if member == group {
			return true
		}
	}
	return false
}

// MaxGroupKeyBytes is the routing_groups.key column width. Keys stay byte-exact
// (the column is VARBINARY) so the limit is in bytes, not runes.
const MaxGroupKeyBytes = 1024

// ValidNewGroupKey reports whether a key may be created explicitly. Legacy keys
// are preserved byte-for-byte by the backfill, but a new key must be clean:
// stated without surrounding whitespace, free of the CSV separator (a key with
// a comma could never be referenced from a resource membership field), free of
// control characters and within the column width.
func ValidNewGroupKey(key string) bool {
	if key == "" || key != strings.TrimSpace(key) || len(key) > MaxGroupKeyBytes {
		return false
	}
	for _, r := range key {
		if r == ',' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidGroupAccessMode accepts the two operator-selectable access modes. An
// empty mode means "restricted", the safe default for a freshly created group.
func ValidGroupAccessMode(mode string) bool {
	return mode == "" || mode == "restricted" || mode == "public"
}
