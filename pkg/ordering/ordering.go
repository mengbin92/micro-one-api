package ordering

import (
	"fmt"
	"strings"
)

type Field struct {
	Name string
	Desc bool
}

func Parse(input string, allowed ...string) ([]Field, error) {
	fields := map[string]bool{}
	for _, f := range allowed {
		fields[f] = true
	}
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}
	result := []Field{}
	seen := map[string]bool{}
	for _, term := range strings.Split(input, ",") {
		parts := strings.Fields(term)
		if len(parts) < 1 || len(parts) > 2 || !fields[parts[0]] || seen[parts[0]] {
			return nil, fmt.Errorf("invalid order_by")
		}
		seen[parts[0]] = true
		field := Field{Name: parts[0]}
		if len(parts) == 2 {
			if parts[1] != "asc" && parts[1] != "desc" {
				return nil, fmt.Errorf("invalid sort direction")
			}
			field.Desc = parts[1] == "desc"
		}
		result = append(result, field)
	}
	return result, nil
}
