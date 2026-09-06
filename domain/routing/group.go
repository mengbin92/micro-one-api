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
