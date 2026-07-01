package quota

import (
	"encoding/json"
	"strings"
	"time"
)

type CodexSnapshot struct {
	PrimaryUsedPercent          *float64
	PrimaryResetAfterSeconds    *int
	PrimaryWindowMinutes        *int
	SecondaryUsedPercent        *float64
	SecondaryResetAfterSeconds  *int
	SecondaryWindowMinutes      *int
	PrimaryOverSecondaryPercent *float64
	UpdatedAt                   time.Time
}

func ParseCodexSnapshot(body []byte, now time.Time) (*CodexSnapshot, bool) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false
	}
	candidates := make([]windowCandidate, 0, 2)
	var ratio *float64
	walk(root, nil, &candidates, &ratio)
	if len(candidates) == 0 && ratio == nil {
		return nil, false
	}
	snapshot := &CodexSnapshot{PrimaryOverSecondaryPercent: ratio, UpdatedAt: now}
	for _, candidate := range candidates {
		if candidate.isPrimary() && snapshot.PrimaryUsedPercent == nil {
			snapshot.PrimaryUsedPercent = &candidate.usedPercent
			snapshot.PrimaryResetAfterSeconds = candidate.resetAfterSeconds
			snapshot.PrimaryWindowMinutes = candidate.windowMinutes
			continue
		}
		if candidate.isSecondary() && snapshot.SecondaryUsedPercent == nil {
			snapshot.SecondaryUsedPercent = &candidate.usedPercent
			snapshot.SecondaryResetAfterSeconds = candidate.resetAfterSeconds
			snapshot.SecondaryWindowMinutes = candidate.windowMinutes
			continue
		}
	}
	if snapshot.PrimaryUsedPercent == nil && len(candidates) > 0 {
		c := candidates[0]
		snapshot.PrimaryUsedPercent = &c.usedPercent
		snapshot.PrimaryResetAfterSeconds = c.resetAfterSeconds
		snapshot.PrimaryWindowMinutes = c.windowMinutes
	}
	if snapshot.SecondaryUsedPercent == nil && len(candidates) > 1 {
		c := candidates[1]
		snapshot.SecondaryUsedPercent = &c.usedPercent
		snapshot.SecondaryResetAfterSeconds = c.resetAfterSeconds
		snapshot.SecondaryWindowMinutes = c.windowMinutes
	}
	return snapshot, true
}

func ShouldAutoPause(snapshot *CodexSnapshot, primaryThreshold, secondaryThreshold float64) bool {
	if snapshot == nil {
		return false
	}
	if primaryThreshold <= 0 {
		primaryThreshold = 95
	}
	if secondaryThreshold <= 0 {
		secondaryThreshold = 100
	}
	if snapshot.PrimaryUsedPercent != nil && *snapshot.PrimaryUsedPercent >= primaryThreshold {
		return true
	}
	return snapshot.SecondaryUsedPercent != nil && *snapshot.SecondaryUsedPercent >= secondaryThreshold
}

type windowCandidate struct {
	path              []string
	usedPercent       float64
	resetAfterSeconds *int
	windowMinutes     *int
}

func (c windowCandidate) isPrimary() bool {
	for _, part := range c.path {
		if strings.Contains(normalize(part), "primary") {
			return true
		}
	}
	return c.windowMinutes != nil && *c.windowMinutes <= 300
}

func (c windowCandidate) isSecondary() bool {
	for _, part := range c.path {
		if strings.Contains(normalize(part), "secondary") {
			return true
		}
	}
	return c.windowMinutes != nil && *c.windowMinutes >= 10080
}

func walk(value any, path []string, candidates *[]windowCandidate, ratio **float64) {
	switch typed := value.(type) {
	case map[string]any:
		if v, ok := numberField(typed, "primary_over_secondary_percent"); ok {
			copy := v
			*ratio = &copy
		}
		if used, ok := numberField(typed, "used_percent"); ok {
			candidate := windowCandidate{path: append([]string(nil), path...), usedPercent: used}
			if reset, ok := numberField(typed, "reset_after_seconds"); ok {
				value := int(reset)
				candidate.resetAfterSeconds = &value
			}
			if window, ok := numberField(typed, "window_minutes"); ok {
				value := int(window)
				candidate.windowMinutes = &value
			}
			*candidates = append(*candidates, candidate)
		}
		for key, child := range typed {
			walk(child, append(path, key), candidates, ratio)
		}
	case []any:
		for _, child := range typed {
			walk(child, path, candidates, ratio)
		}
	}
}

func numberField(values map[string]any, name string) (float64, bool) {
	target := normalize(name)
	for key, value := range values {
		if normalize(key) != target {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed, true
		case int:
			return float64(typed), true
		}
	}
	return 0, false
}

func normalize(input string) string {
	replacer := strings.NewReplacer("_", "", "-", "", ".", "")
	return strings.ToLower(replacer.Replace(input))
}
