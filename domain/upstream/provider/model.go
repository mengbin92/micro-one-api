package provider

import (
	"strings"

	"micro-one-api/pkg/jsonx"
	"micro-one-api/pkg/wildcard"
)

// ModelName strips client-only extended-context hints before routing upstream.
func ModelName(model string) string {
	model = strings.TrimSpace(model)
	const suffix = "[1m]"
	if len(model) >= len(suffix) && strings.EqualFold(model[len(model)-len(suffix):], suffix) {
		return strings.TrimSpace(model[:len(model)-len(suffix)])
	}
	return model
}

// ResolveModel uses the same mapping and model spelling at selection and execution.
func ResolveModel(model, mapping, upstreamModel string, configuredModels []string) string {
	model = ModelName(model)
	if mapped, ok := resolveModelMapping(mapping, model); ok {
		return ModelName(mapped)
	}
	if upstream := strings.TrimSpace(upstreamModel); upstream != "" {
		return ModelName(upstream)
	}
	for _, configured := range configuredModels {
		configured = ModelName(configured)
		if configured != "" && !wildcard.IsPattern(configured) && strings.EqualFold(configured, model) {
			return configured
		}
	}
	return model
}

func resolveModelMapping(mappingJSON, model string) (string, bool) {
	model = ModelName(model)
	mappingJSON = strings.TrimSpace(mappingJSON)
	if mappingJSON == "" {
		return "", false
	}
	var mapping map[string]string
	if err := jsonx.UnmarshalFromString(mappingJSON, &mapping); err != nil {
		return "", false
	}
	// 1) Exact (case-insensitive) match — fast path.
	if dst, ok := mapping[model]; ok && dst != "" {
		return dst, true
	}
	if dst, ok := mapping[strings.ToLower(model)]; ok && dst != "" {
		return dst, true
	}
	// A JSON object may contain mixed-case keys. Map lookup cannot express a
	// true case-insensitive comparison, so finish the exact pass explicitly.
	for key, dst := range mapping {
		if !wildcard.IsPattern(key) && dst != "" && strings.EqualFold(ModelName(key), model) {
			return dst, true
		}
	}
	// 2) Wildcard keys: specific patterns before the "*" catch-all. When
	// several specific patterns match, pick the MOST SPECIFIC one (by
	// non-wildcard char count, ties by full length) so per-account
	// remapping is deterministic — same as ModelMapper.Resolve. See
	// docs/model-management-design.md §11.1.
	var catchAll string
	var bestSpecific string
	var bestSpecificKey string
	var bestSpecificity int
	for key, dst := range mapping {
		if !wildcard.IsPattern(key) || dst == "" {
			continue
		}
		if key == "*" {
			catchAll = dst
			continue
		}
		if !wildcard.Match(key, model) {
			continue
		}
		spec := wildcard.Specificity(key)
		if spec > bestSpecificity || (spec == bestSpecificity && len(key) > len(bestSpecificKey)) {
			bestSpecificity = spec
			bestSpecific = dst
			bestSpecificKey = key
		}
	}
	if bestSpecific != "" {
		return bestSpecific, true
	}
	if catchAll != "" {
		return catchAll, true
	}
	return "", false
}
