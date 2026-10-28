package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	billingdomain "micro-one-api/domain/billing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

var errServiceTierRequest = errors.New("invalid or unsupported service_tier request")

// readServiceTierRequest always records the requested value, including an
// omitted value. The Billing owner decides whether this source/model enables
// tier pricing; an unsupported ingress must therefore send its protocol too.
func readServiceTierRequest(body []byte, protocol string) (billingdomain.ServiceTierRequest, error) {
	request := billingdomain.ServiceTierRequest{Protocol: protocol, Version: billingdomain.ServiceTierPricingVersion}
	var payload map[string]jsonx.RawMessage
	if jsonx.Unmarshal(body, &payload) != nil {
		return request, nil // The handler owns ordinary JSON validation.
	}
	if raw, ok := payload["service_tier"]; ok {
		if jsonx.Unmarshal(raw, &request.RequestedServiceTier) != nil {
			return request, errServiceTierRequest
		}
		switch request.RequestedServiceTier {
		case "auto", "default", "priority", "flex":
		default:
			return request, errServiceTierRequest
		}
	}
	if protocol != "openai_chat" && protocol != "openai_responses" {
		return request, nil
	}
	// Only self-contained textual requests have a byte-derived input bound.
	// Hidden conversation state, tools, files and remote media can add input
	// tokens independently of the submitted bytes and are outside this MVP.
	for _, key := range []string{"previous_response_id", "conversation", "prompt", "tools", "tool_choice", "background"} {
		if raw, ok := payload[key]; ok && string(raw) != "null" && string(raw) != "false" {
			return request, nil
		}
	}
	if raw, ok := payload["instructions"]; ok {
		var instructions string
		if jsonx.Unmarshal(raw, &instructions) != nil {
			return request, nil
		}
	}
	if raw, ok := payload["n"]; ok && string(raw) != "1" {
		return request, nil
	}
	inputKey, limitKey := "messages", "max_completion_tokens"
	if protocol == "openai_responses" {
		inputKey, limitKey = "input", "max_output_tokens"
	}
	if !textOnlyTierInput(payload[inputKey], protocol) {
		return request, nil
	}
	if jsonx.Unmarshal(payload[limitKey], &request.OutputTokenBound) != nil || request.OutputTokenBound <= 0 {
		return request, nil
	}
	// A UTF-8 byte upper bound dominates text token counts; the additional
	// per-message allowance covers protocol framing/role tokens conservatively.
	request.InputTokenBound = int64(len(body))*2 + 4096
	return request, nil
}

func textOnlyTierInput(raw []byte, protocol string) bool {
	var text string
	if protocol == "openai_responses" && jsonx.Unmarshal(raw, &text) == nil {
		return true
	}
	var messages []map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return false
	}
	for _, message := range messages {
		for key := range message {
			if key != "role" && key != "content" && key != "type" {
				return false
			}
		}
		var role string
		if jsonx.Unmarshal(message["role"], &role) != nil || (role != "user" && role != "assistant" && role != "system" && role != "developer") {
			return false
		}
		if jsonx.Unmarshal(message["content"], &text) == nil {
			continue
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if protocol != "openai_responses" || jsonx.Unmarshal(message["content"], &parts) != nil || len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if part.Type != "input_text" && part.Type != "output_text" {
				return false
			}
		}
	}
	return true
}

func tierAttemptContext(ctx context.Context, body []byte, protocol string, channel *relaybiz.Channel) context.Context {
	if channel == nil || channel.Type != relayprovider.ChannelTypeOpenAI || channel.SubscriptionAccountID != 0 {
		protocol = "unsupported"
	}
	request, _ := readServiceTierRequest(body, protocol)
	return billingdomain.WithServiceTierRequest(ctx, request)
}

func rejectUnsupportedServiceTier(ctx context.Context) error {
	request := billingdomain.GetServiceTierRequest(ctx)
	if request.RequestedServiceTier == "priority" || request.RequestedServiceTier == "flex" {
		return errServiceTierRequest
	}
	return nil
}

// Billing decides admission for an enabled tier-priced source after Relay has
// already accepted the request. Its rejections are client errors (the request
// shape or entry point is out of scope), not a quota shortfall, so they must
// not be reported as 402 or counted against the source. The reasons cross gRPC
// inside the status message, which is what this matches.
var serviceTierAdmissionReasons = map[string]string{
	"SERVICE_TIER_UNSUPPORTED":    "service_tier is not supported for this model or entry point",
	"SERVICE_TIER_BOUND_REQUIRED": "service_tier requires a text-only request with max_completion_tokens or max_output_tokens",
	"SERVICE_TIER_BOUND_EXCEEDED": "service_tier usage exceeds the reserved token bound",
}

// serviceTierAdmissionMessage returns a client-safe explanation for a Billing
// service-tier rejection. Only the fixed reason table is exposed; the wrapped
// upstream detail stays in logs.
func serviceTierAdmissionMessage(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	// A rejection classified as a different upstream status is not an
	// admission failure, even if its text happens to quote a reason constant.
	if status := relaybiz.UpstreamStatus(err); status != 0 && status != http.StatusBadRequest {
		return "", false
	}
	message := err.Error()
	if st, ok := status.FromError(err); ok {
		message += " " + st.Message()
	}
	for reason, clientMessage := range serviceTierAdmissionReasons {
		if strings.Contains(message, reason) {
			return clientMessage, true
		}
	}
	return "", false
}

// writeServiceTierAdmissionError reports a tier rejection as 400 with the
// field named, so clients can tell a bad request from insufficient quota.
func (s *HTTPServer) writeServiceTierAdmissionError(w http.ResponseWriter, err error) bool {
	message, ok := serviceTierAdmissionMessage(err)
	if !ok {
		return false
	}
	s.writeError(w, http.StatusBadRequest, message)
	return true
}

// tierObservation retains an upstream-reported value separately from the
// request. Empty, future and conflicting values remain explicit evidence gaps.
type tierObservation struct {
	actual  string
	status  string
	invalid bool
}

func (t *tierObservation) observe(value string) {
	// Once conflicting or unrecordable evidence has been observed, later
	// chunks cannot restore a cheaper reported tier. Both states settle using
	// the frozen maximum-price policy for the entire upstream attempt.
	if t.status == "conflict" || t.invalid {
		return
	}
	if len(value) > 32 {
		t.actual, t.status = "", "unknown"
		t.invalid = true
		return
	}
	if value == "" {
		return
	}
	if t.actual != "" && t.actual != value {
		t.actual, t.status = "", "conflict"
		return
	}
	t.actual, t.status = value, "reported"
	if value != "default" && value != "priority" && value != "flex" {
		t.status = "unknown"
	}
}

func (t *tierObservation) observeJSON(body []byte) {
	var payload struct {
		Tier     jsonx.RawMessage `json:"service_tier"`
		Response *struct {
			Tier jsonx.RawMessage `json:"service_tier"`
		} `json:"response"`
	}
	if jsonx.Unmarshal(body, &payload) != nil {
		return
	}
	t.observeRaw(payload.Tier)
	if payload.Response != nil {
		t.observeRaw(payload.Response.Tier)
	}
}

func (t *tierObservation) observeRaw(raw jsonx.RawMessage) {
	if len(raw) == 0 || t.status == "conflict" || t.invalid {
		return
	}
	var value *string
	if jsonx.Unmarshal(raw, &value) != nil || value == nil || *value == "" {
		t.actual, t.status, t.invalid = "", "unknown", true
		return
	}
	t.observe(*value)
}

// Typed OpenAI parsers preserve raw detail integers, while legacy conversion
// helpers clamp negative details. Reject these before that clamp can turn
// unproven usage into tier-priced verified buckets.
func envelopeFromCompleteTierChatUsage(u relayprovider.Usage, canonical *relayprovider.CanonicalUsage) relaybiz.UsageEnvelope {
	env := envelopeFromProviderUsage(u, canonical)
	for _, details := range []relayprovider.UsageTokenDetails{u.PromptTokensDetails, u.InputTokensDetails} {
		if details.CachedTokens < 0 || details.CacheReadTokens < 0 || details.CacheCreation5mTokens < 0 || details.CacheCreation1hTokens < 0 {
			env.ParseStatus, env.Canonical, env.DecisionReason = relaybiz.UsageParseAmbiguous, nil, relaybiz.UsageReasonNegativeBucket
			return env
		}
	}
	if env.ParseStatus == relaybiz.UsageParseEstimated && u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		env.ParseStatus, env.Semantics = relaybiz.UsageParseVerified, relaybiz.UsageSemanticsOpenAISubset
		env.Canonical = &relaybiz.CanonicalUsage{}
	}
	return env
}

func (in *usageLogInput) applyTier(ctx context.Context, observation tierObservation) {
	in.RequestedServiceTier = billingdomain.GetServiceTierRequest(ctx).RequestedServiceTier
	in.ActualServiceTier, in.ServiceTierStatus = observation.actual, observation.status
	if strings.TrimSpace(in.ServiceTierStatus) == "" {
		in.ServiceTierStatus = "missing"
	}
}

// Tier responses never use estimates as usage evidence. Required token fields
// are pointers so an explicitly reported zero stays distinct from absence.
func completeTierResponsesUsage(body []byte) (rawUsage, bool) {
	var payload struct {
		Usage    jsonx.RawMessage `json:"usage"`
		Response *struct {
			Usage jsonx.RawMessage `json:"usage"`
		} `json:"response"`
	}
	if jsonx.Unmarshal(body, &payload) != nil {
		return rawUsage{}, false
	}
	raw := payload.Usage
	if payload.Response != nil {
		raw = payload.Response.Usage
	}
	if !validTierUsageNumbers(raw) {
		return rawUsage{}, false
	}
	var reported struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
		Total  *int64 `json:"total_tokens"`
	}
	if jsonx.Unmarshal(raw, &reported) != nil || reported.Input == nil || reported.Output == nil || *reported.Input < 0 || *reported.Output < 0 || reported.Total != nil && *reported.Total < 0 {
		return rawUsage{}, false
	}
	u := extractRawUsage(raw, 0)
	u.PromptTokens, u.CompletionTokens = *reported.Input, *reported.Output
	u.Shape.HasInputTokens = true
	return u, true
}

// The legacy raw parser intentionally tolerates/coerces provider values.
// Tier settlement requires every supplied token count to be an integer;
// strings, fractions and null cache details are not evidence of zero usage.
func validTierUsageNumbers(raw jsonx.RawMessage) bool {
	var fields map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key, value := range fields {
		if strings.HasSuffix(key, "_tokens") {
			var count *int64
			if jsonx.Unmarshal(value, &count) != nil || count == nil || *count < 0 {
				return false
			}
		} else if strings.HasSuffix(key, "_tokens_details") || key == "cache_creation" {
			if !validTierUsageNumbers(value) {
				return false
			}
		}
	}
	return true
}

// The caller has proven completeTierResponsesUsage, including the all-zero
// case that the generic legacy parser otherwise treats as estimator input.
func envelopeFromCompleteTierResponsesUsage(u rawUsage) relaybiz.UsageEnvelope {
	env := envelopeFromRawUsage(u)
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.CacheReadTokens == 0 && u.CacheCreation5mTokens == 0 && u.CacheCreation1hTokens == 0 && u.ReportedTotalTokens == 0 && u.Shape.InvalidReason == "" {
		env.ParseStatus = relaybiz.UsageParseVerified
		env.Semantics = relaybiz.UsageSemanticsOpenAISubset
		env.Canonical = &relaybiz.CanonicalUsage{}
	}
	return env
}

func tierAttemptLog(plan *relaybiz.RelayPlan, channel *relaybiz.Channel, reservation *billingv1.ReserveQuotaResponse, billingModel, endpoint string, stream bool, started time.Time) usageLogInput {
	in := usageLogInput{UserID: plan.Auth.UserID, TokenID: plan.Auth.TokenID, TokenName: plan.Auth.TokenName, ModelName: billingModel, Endpoint: endpoint, ChannelID: channel.ID, IsStream: stream, ElapsedTime: time.Since(started).Milliseconds()}
	in.applyChannelInputs(channel)
	in.applyReservation(reservation)
	return in
}

// Unproven usage is released rather than estimated for tier-priced requests.
// The zero-quota log retains the separate request/response observations and
// the release outcome, so cancellation never erases upstream tier evidence.
func (s *HTTPServer) releaseTierQuota(ctx context.Context, reservation *billingv1.ReserveQuotaResponse, in usageLogInput, observation tierObservation, reason string) error {
	err := s.releaseQuota(ctx, reservation.ReservationId, reason)
	if reservation.ServiceTierPricingVersion != 1 {
		return err
	}
	in.applyReservation(reservation)
	in.applyTier(ctx, observation)
	in.Quota, in.PromptTokens, in.CompletionTokens = 0, 0, 0
	in.CacheReadTokens, in.CacheCreation5mTokens, in.CacheCreation1hTokens = 0, 0, 0
	in.Usage = nil
	in.SettlementOutcome = "released/" + reason
	if err != nil {
		in.SettlementOutcome = "release_pending/" + reason
	}
	s.ingestUsageLogAfterResponse(in, ctx)
	return err
}
