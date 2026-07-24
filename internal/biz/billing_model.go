package biz

import "strings"

// BillingModelSource (P3 #6) controls which model name is used for billing
// (quota reservation + usage stats) when relay rewrites the client-facing
// model name. Mirrors sub2api BillingModelSource.
//
//   - "requested"     (default): bill on the client-requested model name
//     (RelayRequest.Model), before any mapping. Legacy behaviour.
//   - "upstream":      bill on the final upstream model name (RelayPlan.
//     ResolvedModel), after global ModelMapper + per-account/channel
//     mapping. Use when pricing/quotas are keyed by the upstream provider's
//     real model id.
//   - "channel_mapped": bill on the channel/account's own mapped name in
//     isolation (applyPerAccountModelMapping on the original client model,
//     skipping the global mapper). Use when pricing differs per channel but
//     not per global alias.
//
// See docs/model-management-design.md §9.3 #6 / §9.4 P3.

const (
	BillingModelSourceRequested     = "requested"
	BillingModelSourceUpstream      = "upstream"
	BillingModelSourceChannelMapped = "channel_mapped"
)

// BillingModelForSource computes the billing model name given the three
// candidate values and the configured source. It is a pure function so the
// precedence is owned by the domain and unit-testable.
//
//   - clientModel:   the model name the client sent (RelayRequest.Model)
//   - resolvedModel: the model after global ModelMapper (but before
//     per-account/channel mapping)
//   - upstreamModel: the final model sent upstream (after all mapping;
//     RelayPlan.ResolvedModel)
//
// The channel_mapped source uses resolvedModel as the base for the
// per-account mapping lookup because the channel's ModelMapping JSON is
// applied on top of the globally-resolved name in Plan() — using the
// resolved name keeps the lookup consistent with the selection path.
func BillingModelForSource(source, clientModel, resolvedModel, upstreamModel string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case BillingModelSourceUpstream:
		if upstreamModel != "" {
			return upstreamModel
		}
		return clientModel
	case BillingModelSourceChannelMapped:
		// The channel-mapped name is the per-account/channel mapping applied
		// to the resolved model; upstreamModel already carries it. Fall back
		// to resolved, then client when empty.
		//
		// TODO(P3 #6): channel_mapped is currently equivalent to upstream
		// because all server call sites pass upstreamModel = plan.ResolvedModel
		// (which already carries the per-account mapping). To make it truly
		// distinct (skip the global mapper, apply only the channel's mapping),
		// the call sites must thread a separately-computed channel-mapped name.
		// Tracked as a follow-up; see docs/model-management-design.md §13.
		if upstreamModel != "" {
			return upstreamModel
		}
		if resolvedModel != "" {
			return resolvedModel
		}
		return clientModel
	default:
		// "requested" (and any unknown/empty value): bill on client model.
		return clientModel
	}
}
