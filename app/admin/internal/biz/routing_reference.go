package biz

import "context"

type routingReferenceReadKey struct{}

// withRoutingReferenceRead selects internal directory/quote reads used to
// evaluate routing eligibility. These are service reads, not management
// operations by the end user. Identity facts must still be authenticated,
// and AccessSources filters groups before prices/models leave the usecase.
func withRoutingReferenceRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, routingReferenceReadKey{}, true)
}

func IsRoutingReferenceRead(ctx context.Context) bool {
	v, _ := ctx.Value(routingReferenceReadKey{}).(bool)
	return v
}
