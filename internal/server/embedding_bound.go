package server

import (
	"micro-one-api/domain/routing"
	provider "micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
)

// OpenAI text embeddings have no generated output/tool charges. Byte length is
// a conservative token bound for these byte-level tokenizers; allow additional
// boundary tokens per input. Unknown models, modalities and options stay closed.
func embeddingCostBound(path string, channelType int32, model string, body []byte) routing.CostBound {
	empty := routing.CostBound{}
	if path != "/embeddings" || (channelType != provider.ChannelTypeOpenAI && channelType != provider.ChannelTypeAzure) {
		return empty
	}
	var fields map[string]jsonx.RawMessage
	if jsonx.Unmarshal(body, &fields) != nil {
		return empty
	}
	for key := range fields {
		switch key {
		case "model", "input", "user", "encoding_format", "dimensions":
		default:
			return empty
		}
	}
	var inputs []string
	var single string
	if jsonx.Unmarshal(fields["input"], &single) == nil {
		inputs = []string{single}
	} else if jsonx.Unmarshal(fields["input"], &inputs) != nil {
		return empty
	}
	if len(inputs) == 0 || len(inputs) > 2048 {
		return empty
	}
	var tokens int64
	for _, input := range inputs {
		if input == "" {
			return empty
		}
		tokens += int64(len(input)) + 16
	}
	b := routing.CostBound{Protocol: "openai_text_embeddings", InputTokens: tokens, UpstreamModel: model}
	if !b.Valid() {
		return empty
	}
	return b
}
