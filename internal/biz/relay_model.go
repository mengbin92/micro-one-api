package biz

import "strings"

const extendedContextModelSuffix = "[1m]"

// RelayModelName returns the real model identifier used for routing, billing,
// and upstream requests. A terminal [1M] marker is a client-side hint that
// enables the extended context window; it is not part of the upstream model
// identifier.
func RelayModelName(model string) string {
	model = strings.TrimSpace(model)
	if len(model) >= len(extendedContextModelSuffix) &&
		strings.EqualFold(model[len(model)-len(extendedContextModelSuffix):], extendedContextModelSuffix) {
		return strings.TrimSpace(model[:len(model)-len(extendedContextModelSuffix)])
	}
	return model
}
