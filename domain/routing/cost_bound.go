package routing

import "context"

// CostBound is produced by a trusted protocol adapter, never decoded from a
// public request as billing authority. Only bounded text embeddings are enabled.
type CostBound struct {
	Protocol      string
	InputTokens   int64
	UpstreamModel string
}
type costBoundKey struct{}

func WithCostBound(ctx context.Context, b CostBound) context.Context {
	return context.WithValue(ctx, costBoundKey{}, b)
}
func GetCostBound(ctx context.Context) CostBound {
	v, _ := ctx.Value(costBoundKey{}).(CostBound)
	return v
}
func (b CostBound) Valid() bool {
	if b.Protocol != "openai_text_embeddings" || b.InputTokens <= 0 || b.InputTokens > 2_000_000 {
		return false
	}
	return b.UpstreamModel == "text-embedding-3-small" || b.UpstreamModel == "text-embedding-3-large" || b.UpstreamModel == "text-embedding-ada-002"
}
