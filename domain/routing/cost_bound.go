package routing

import "context"

// CostBound is produced by a trusted protocol adapter, never decoded from a
// public request as billing authority. Audio input uses separate measured units.
type CostBound struct {
	Protocol      string
	InputTokens   int64
	UpstreamModel string
	AudioUnits    int64 `json:",omitempty"`
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
	if AudioInputProtocol(b.UpstreamModel) != "" {
		return b.AudioUnitsPerPrice() > 0
	}
	if b.Protocol != "openai_text_embeddings" || b.AudioUnits != 0 || b.InputTokens <= 0 || b.InputTokens > 2_000_000 {
		return false
	}
	return b.UpstreamModel == "text-embedding-3-small" || b.UpstreamModel == "text-embedding-3-large" || b.UpstreamModel == "text-embedding-ada-002"
}

// Audio input is metered in half-characters for synthesis and microseconds for
// transcription. The protocol and upstream model must agree.
func AudioInputProtocol(model string) string {
	switch model {
	case "stepaudio-2.5-tts":
		return "stepaudio_tts"
	case "stepaudio-2.5-asr":
		return "stepaudio_asr_sse"
	default:
		return ""
	}
}

func (b CostBound) AudioUnitsPerPrice() int64 {
	if b.AudioUnits <= 0 || b.InputTokens != 0 || b.Protocol != AudioInputProtocol(b.UpstreamModel) {
		return 0
	}
	switch b.Protocol {
	case "stepaudio_tts":
		if b.AudioUnits <= 2_000 {
			return 2_000_000
		}
	case "stepaudio_asr_sse":
		if b.AudioUnits <= 3_600_000_000 {
			return 3_600_000_000
		}
	}
	return 0
}
