package biz

import "micro-one-api/domain/upstream/provider"

// RelayModelName returns the real model identifier used for routing, billing,
// and upstream requests. A terminal [1M] marker is a client-side hint that
// enables the extended context window; it is not part of the upstream model
// identifier.
func RelayModelName(model string) string { return provider.ModelName(model) }
