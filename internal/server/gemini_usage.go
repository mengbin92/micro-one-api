package server

import (
	"strconv"
	"strings"

	"micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/gemini"
)

func validateNativeGeminiPlan(endpoint APIEndpoint, plan *relaybiz.RelayPlan) error {
	if endpoint != EndpointGemini {
		return nil
	}
	if plan.Channel.Type != provider.ChannelTypeGemini || plan.Account != nil || plan.Channel.SubscriptionAccountID != 0 || !gemini.ValidModel(plan.ResolvedModel) {
		return relaybiz.MarkProtocolCapabilityError(provider.BeforeForward(&provider.CapabilityError{Feature: "native Gemini requires an API-key Gemini channel and a valid mapped model"}))
	}
	kind, model := upstreamCostKeyInputsFromPlan(plan)
	if executionPilot(kind, strconv.FormatInt(plan.Channel.ID, 10), 0, model) {
		return provider.BeforeForward(&provider.CapabilityError{Feature: "native Gemini durable execution pilot is outside B2"})
	}
	return nil
}

func geminiUsageEnvelope(u gemini.Usage) relaybiz.UsageEnvelope {
	return relaybiz.UsageEnvelope{
		ContractVersion: relaybiz.UsageContractVersionV1, ParseStatus: relaybiz.UsageParseVerified,
		Semantics: relaybiz.UsageSemanticsOpenAISubset,
		Reported: relaybiz.ReportedUsage{PromptTokens: u.Prompt, OutputTokens: u.Candidates + u.Thoughts,
			CacheReadTokens: u.Cached, TotalTokens: u.Total, SourceProtocol: "gemini",
			FieldShape: "usageMetadata.prompt/cached/candidates+thoughts"},
		Canonical: &relaybiz.CanonicalUsage{UncachedInputTokens: u.Prompt - u.Cached, CacheReadTokens: u.Cached, OutputTokens: u.Candidates + u.Thoughts},
	}
}

// The adaptor emits one validated JSON data line per event. Preserve the latest
// complete snapshot by presence (including explicit zero), never sum chunks.
type geminiStreamUsageObserver struct {
	pending string
	usage   *gemini.Usage
	id      string
}

func (t *geminiStreamUsageObserver) ObserveBytes(p []byte) {
	t.pending += string(p)
	for {
		line, rest, ok := strings.Cut(t.pending, "\n")
		if !ok {
			break
		}
		t.pending = rest
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		frame, err := gemini.ParseResponse([]byte(strings.TrimSpace(data)))
		if err != nil {
			continue
		}
		if frame.Usage != nil {
			t.usage = frame.Usage
		}
		if frame.ID != "" {
			t.id = frame.ID
		}
	}
}
func (t *geminiStreamUsageObserver) Usage() rawUsage    { return rawUsage{} }
func (t *geminiStreamUsageObserver) SawUsage() bool     { return t.usage != nil }
func (t *geminiStreamUsageObserver) ResponseID() string { return t.id }

func nativeGeminiRequestBody(endpoint APIEndpoint, body []byte, model string) []byte {
	if endpoint == EndpointGemini {
		return body
	}
	return rewriteRequestModel(body, model)
}

func estimateEndpointUsage(endpoint APIEndpoint, body []byte) relaybiz.UsageEnvelope {
	if endpoint != EndpointGemini {
		return estimateUsageFromBody(body)
	}
	_, output, _ := gemini.PrepareRequest(body)
	return relaybiz.UsageEnvelope{ContractVersion: relaybiz.UsageContractVersionV1, ParseStatus: relaybiz.UsageParseEstimated,
		Canonical: &relaybiz.CanonicalUsage{UncachedInputTokens: int64(len(body))*2 + 4096, OutputTokens: output}}
}
