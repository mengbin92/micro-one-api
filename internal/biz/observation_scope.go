package biz

import (
	"fmt"
	"strconv"
	"strings"
)

// ObservationScope is an observation-only rollout boundary. It is deliberately
// independent of RELAY_NATIVE_USAGE_V2_ALLOWLIST: narrowing observation never
// changes settlement, and widening settlement never widens observation.
//
// Format: comma separated `kind:id:model` entries, where kind is `channel` or
// `subscription`, id is numeric, and model may be `*`. A single `*` entry
// allows every source. An empty value means "all sources" because the producer
// master switch is already the coarse gate.
type ObservationScope struct {
	all     bool
	entries map[string]bool
}

func NewObservationScope(csv string) (*ObservationScope, error) {
	scope := &ObservationScope{entries: map[string]bool{}}
	trimmed := strings.TrimSpace(csv)
	if trimmed == "" {
		return scope, nil
	}
	for _, entry := range strings.Split(trimmed, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("observation scope contains an empty entry")
		}
		if entry == "*" {
			scope.all = true
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("observation scope entry %q must be kind:id:model", entry)
		}
		kind, model := parts[0], parts[2]
		if kind != "channel" && kind != "subscription" {
			return nil, fmt.Errorf("observation scope entry %q has unknown source kind", entry)
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("observation scope entry %q has invalid source id", entry)
		}
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("observation scope entry %q is missing a model", entry)
		}
		scope.entries[fmt.Sprintf("%s:%d:%s", kind, id, model)] = true
	}
	return scope, nil
}

// Allows decides whether a send belongs to the configured observation range.
func (s *ObservationScope) Allows(kind string, id int64, model string) bool {
	if s == nil || s.all || len(s.entries) == 0 {
		return true
	}
	if s.entries[fmt.Sprintf("%s:%d:%s", kind, id, model)] {
		return true
	}
	return s.entries[fmt.Sprintf("%s:%d:*", kind, id)]
}

// Empty reports whether the scope constrains anything.
func (s *ObservationScope) Empty() bool { return s == nil || (!s.all && len(s.entries) == 0) }
