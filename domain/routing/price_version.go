package routing

import "context"

type priceVersionKey struct{}

func WithExpectedPriceVersion(ctx context.Context, v int64) context.Context {
	return context.WithValue(ctx, priceVersionKey{}, v)
}
func ExpectedPriceVersion(ctx context.Context) *int64 {
	v, ok := ctx.Value(priceVersionKey{}).(int64)
	if !ok {
		return nil
	}
	return &v
}

type groupExpectedRevisionKey struct{}

func WithExpectedGroupRevision(ctx context.Context, v int64) context.Context {
	return context.WithValue(ctx, groupExpectedRevisionKey{}, v)
}
func ExpectedGroupRevision(ctx context.Context) int64 {
	v, _ := ctx.Value(groupExpectedRevisionKey{}).(int64)
	return v
}
