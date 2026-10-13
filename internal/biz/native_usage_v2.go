package biz

import (
	"bytes"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"strings"
)

func NativeUsageV2(r *evidence.Record) UsageEnvelope {
	u := UsageEnvelope{ContractVersion: 2, ParseStatus: UsageParseStatus("missing"), DecisionReason: "native_usage_missing"}
	if r == nil {
		return u
	}
	o := r.Observation
	u.ParseStatus = UsageParseStatus(o.Status)
	u.DecisionReason = o.Reason
	u.Reported.SourceProtocol = o.Protocol
	var raw map[string]any
	decoder := jsonx.NewDecoder(bytes.NewReader(o.Usage))
	decoder.UseNumber()
	if decoder.Decode(&raw) == nil {
		number := func(key string) int64 {
			n, ok := raw[key].(jsonx.Number)
			if !ok {
				return 0
			}
			v, err := n.Int64()
			if err != nil || v < 0 {
				return 0
			}
			return v
		}
		switch o.Protocol {
		case "openai_chat":
			u.Reported.PromptTokens = number("prompt_tokens")
			u.Reported.OutputTokens = number("completion_tokens")
			u.Reported.TotalTokens = number("total_tokens")
		case "responses":
			u.Reported.PromptTokens = number("input_tokens")
			u.Reported.OutputTokens = number("output_tokens")
			u.Reported.TotalTokens = number("total_tokens")
		case "anthropic_messages":
			u.Reported.PromptTokens = number("input_tokens")
			u.Reported.OutputTokens = number("output_tokens")
		case "gemini":
			u.Reported.PromptTokens = number("promptTokenCount")
			u.Reported.OutputTokens = number("candidatesTokenCount")
			u.Reported.TotalTokens = number("totalTokenCount")
		}
	}
	if o.Buckets != nil {
		u.Reported.CacheReadTokens = o.Buckets[1]
		u.Reported.CacheCreation5mTokens = o.Buckets[2]
		u.Reported.CacheCreation1hTokens = o.Buckets[3]
	}
	u.Reported.FieldShape = strings.Join(o.Present, ",")
	if !r.Complete || r.HTTPStatus != 0 && (r.HTTPStatus < 200 || r.HTTPStatus >= 300) {
		u.ParseStatus = "missing"
		u.DecisionReason = "execution_incomplete"
		return u
	}
	if o.Status == "verified" && o.Buckets != nil {
		b := o.Buckets
		u.Canonical = &CanonicalUsage{UncachedInputTokens: b[0], CacheReadTokens: b[1], CacheCreation5mTokens: b[2], CacheCreation1hTokens: b[3], OutputTokens: b[4]}
		u.Semantics = UsageSemantics(o.Semantics)
	} else if o.Status != "missing" && o.Status != "invalid" && o.Status != "unsupported" {
		u.ParseStatus = "unsupported"
		u.DecisionReason = "native_unobserved"
	}
	return u
}

func NativeBillingUsage(usage billingdomain.LedgerUsage, record *evidence.Record) billingdomain.LedgerUsage {
	u := NativeUsageV2(record)
	usage.UsageContractVersion = 2
	usage.PromptExclusive = u.Semantics == UsageSemanticsAnthropicExclusive
	usage.Envelope = &billingdomain.UsageEnvelopeData{ReportedPromptTokens: u.Reported.PromptTokens, ReportedOutputTokens: u.Reported.OutputTokens, ReportedCacheReadTokens: u.Reported.CacheReadTokens, ReportedCacheCreation5mTokens: u.Reported.CacheCreation5mTokens, ReportedCacheCreation1hTokens: u.Reported.CacheCreation1hTokens, ReportedTotalTokens: u.Reported.TotalTokens, ParseStatus: string(u.ParseStatus), Semantics: string(u.Semantics), Protocol: u.Reported.SourceProtocol, DecisionReason: u.DecisionReason}
	if usage.Envelope.DecisionReason == "" && usage.Envelope.ParseStatus != "verified" {
		usage.Envelope.DecisionReason = "native_" + usage.Envelope.ParseStatus
	}
	usage.PromptTokens, usage.CompletionTokens, usage.CacheReadTokens, usage.CacheCreation5mTokens, usage.CacheCreation1hTokens = 0, 0, 0, 0, 0
	if u.Canonical != nil {
		b := u.Canonical
		usage.Envelope.Canonical = &billingdomain.CanonicalBuckets{UncachedInputTokens: b.UncachedInputTokens, CacheReadTokens: b.CacheReadTokens, CacheCreation5mTokens: b.CacheCreation5mTokens, CacheCreation1hTokens: b.CacheCreation1hTokens, OutputTokens: b.OutputTokens}
		usage.PromptTokens = u.Reported.PromptTokens
		usage.CompletionTokens = b.OutputTokens
		usage.CacheReadTokens = b.CacheReadTokens
		usage.CacheCreation5mTokens = b.CacheCreation5mTokens
		usage.CacheCreation1hTokens = b.CacheCreation1hTokens
	}
	if record != nil {
		usage.Envelope.FieldShape = strings.Join(record.Observation.Present, ",")
		usage.Envelope.ParserRevision = record.Observation.ParserRevision
		usage.Envelope.ContractRevision = record.Observation.ContractRevision
		usage.Envelope.PresentPaths = append([]string(nil), record.Observation.Present...)
	}
	return usage
}
