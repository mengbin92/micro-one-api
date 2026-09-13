// Package filtering parses the documented equality subset of AIP filters.
package filtering

import (
	"fmt"
	"regexp"
	"strings"

	"micro-one-api/pkg/jsonx"
)

var equality = regexp.MustCompile(`^([a-z_]+)\s*=\s*("(?:[^"\\]|\\.)*")`)

func Equalities(input string, allowed ...string) (map[string]string, error) {
	if len(input) > 4096 {
		return nil, fmt.Errorf("filter too long")
	}
	fields := map[string]bool{}
	for _, f := range allowed {
		fields[f] = true
	}
	out := map[string]string{}
	rest := strings.TrimSpace(input)
	for rest != "" {
		match := equality.FindStringSubmatch(rest)
		if match == nil || !fields[match[1]] {
			return nil, fmt.Errorf("unsupported filter")
		}
		if _, exists := out[match[1]]; exists {
			return nil, fmt.Errorf("duplicate filter")
		}
		var value string
		if err := jsonx.Unmarshal([]byte(match[2]), &value); err != nil {
			return nil, fmt.Errorf("invalid filter value")
		}
		out[match[1]] = value
		rest = strings.TrimSpace(rest[len(match[0]):])
		if rest == "" {
			break
		}
		if !strings.HasPrefix(rest, "AND ") {
			return nil, fmt.Errorf("unsupported filter operator")
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "AND "))
		if rest == "" {
			return nil, fmt.Errorf("incomplete filter")
		}
	}
	return out, nil
}
