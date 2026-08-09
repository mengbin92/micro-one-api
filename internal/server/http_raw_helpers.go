package server

import (
	crypto_rand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"

	relayprovider "micro-one-api/domain/upstream/provider"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

// extractRawModel pulls the "model" field out of a JSON request body.
func extractRawModel(body []byte) string {
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return ""
	}
	model, _ := payload["model"].(string)
	return strings.TrimSpace(model)
}

// rewriteRawModel rewrites the model field in a JSON body when it differs from
// the client-facing model. If the body has no "model" field it is returned
// unchanged so callers can rely on the default-model fallback.
func rewriteRawModel(body []byte, model string) []byte {
	model = strings.TrimSpace(model)
	if model == "" {
		return body
	}
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return body
	}
	if _, ok := payload["model"]; !ok {
		return body
	}
	current, _ := payload["model"].(string)
	if strings.TrimSpace(current) == model {
		return body
	}
	payload["model"] = model
	rewritten, err := jsonx.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// ensureRawModel sets the model field on a JSON body, inserting it if absent.
func ensureRawModel(body []byte, model string) []byte {
	model = strings.TrimSpace(model)
	if model == "" {
		return body
	}
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return body
	}
	current, _ := payload["model"].(string)
	if strings.TrimSpace(current) == model {
		return body
	}
	payload["model"] = model
	rewritten, err := jsonx.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// routeResolvedModel returns the resolved model for a stored response route,
// falling back to the original client model when no resolved model is set.
func routeResolvedModel(route responseRoute) string {
	if strings.TrimSpace(route.ResolvedModel) != "" {
		return strings.TrimSpace(route.ResolvedModel)
	}
	return strings.TrimSpace(route.Model)
}

// isRawStreamRequest reports whether the request body requests streaming.
func isRawStreamRequest(body []byte) bool {
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return false
	}
	stream, _ := payload["stream"].(bool)
	return stream
}

// extractPreviousResponseID pulls the previous_response_id from a body.
func extractPreviousResponseID(body []byte) string {
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return ""
	}
	responseID, _ := payload["previous_response_id"].(string)
	responseID = strings.TrimSpace(responseID)
	if !isOpenAIResponseID(responseID) {
		return ""
	}
	return responseID
}

// extractSessionHash pulls the sticky session hash from a JSON request body.
func extractSessionHash(body []byte) string {
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"session_hash", "sessionHash"} {
		sessionHash, _ := payload[key].(string)
		if sessionHash = strings.TrimSpace(sessionHash); sessionHash != "" {
			return sessionHash
		}
	}
	return ""
}

func extractSessionHashFromRequest(r *http.Request, body []byte) string {
	for _, key := range []string{"X-Session-Hash", "OpenAI-Session-Hash"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return extractSessionHash(body)
}

// extractResponseID pulls the top-level "id" field from a body.
func extractResponseID(body []byte) string {
	var payload struct {
		ID string `json:"id"`
	}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ID)
}

// rawUsage holds token usage extracted from a raw upstream response body.
//
// The cache-creation buckets follow docs/design/token-usage-semantics.md
// (v0.11.0 ADR). CacheCreation5mTokens / CacheCreation1hTokens are the
// canonical TTL-split buckets; providers that only return a total
// cache_creation_input_tokens with no ephemeral detail default the whole
// total into the 5m bucket (ADR §4.2). Negative values are clamped to 0 by
// the callers (nonNeg) and recorded via metrics.TokenUsageParseAnomaly.
type rawUsage struct {
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	TotalTokens           int64
}

// extractRawUsage finds the usage block anywhere in a JSON document and
// normalizes it with the supplied fallback when fields are missing.
func extractRawUsage(body []byte, fallback int64) rawUsage {
	var payload interface{}
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return rawUsage{TotalTokens: fallback}
	}
	return normalizeRawUsage(extractRawUsageValue(payload), fallback)
}

// extractRawUsageValue recursively searches an unmarshalled JSON value for a
// usage-like object.
func extractRawUsageValue(value interface{}) rawUsage {
	switch typed := value.(type) {
	case map[string]interface{}:
		var usage rawUsage
		if nested, ok := typed["usage"]; ok {
			usage = extractRawUsageValue(nested)
		}
		fiveM, oneH, _, _ := cacheCreationDetailTokens(typed)
		prompt := numberField(typed, "prompt_tokens", "input_tokens")
		if prompt < 0 {
			recordTokenUsageAnomaly("negative")
			prompt = 0
		}
		completion := numberField(typed, "completion_tokens", "output_tokens")
		if completion < 0 {
			recordTokenUsageAnomaly("negative")
			completion = 0
		}
		usage = mergeRawUsage(usage, rawUsage{
			PromptTokens:          prompt,
			CompletionTokens:      completion,
			CacheReadTokens:       cacheReadTokensFromUsageMap(typed),
			CacheCreation5mTokens: fiveM,
			CacheCreation1hTokens: oneH,
			TotalTokens:           numberField(typed, "total_tokens"),
		})
		if hasRawUsage(usage) {
			return usage
		}
		for _, nested := range typed {
			usage = extractRawUsageValue(nested)
			if hasRawUsage(usage) {
				return usage
			}
		}
	case []interface{}:
		for _, item := range typed {
			usage := extractRawUsageValue(item)
			if hasRawUsage(usage) {
				return usage
			}
		}
	}
	return rawUsage{}
}

// mergeRawUsage fills zero fields in primary from fallback.
func mergeRawUsage(primary, fallback rawUsage) rawUsage {
	if primary.PromptTokens == 0 {
		primary.PromptTokens = fallback.PromptTokens
	}
	if primary.CompletionTokens == 0 {
		primary.CompletionTokens = fallback.CompletionTokens
	}
	if primary.CacheReadTokens == 0 {
		primary.CacheReadTokens = fallback.CacheReadTokens
	}
	if primary.CacheCreation5mTokens == 0 {
		primary.CacheCreation5mTokens = fallback.CacheCreation5mTokens
	}
	if primary.CacheCreation1hTokens == 0 {
		primary.CacheCreation1hTokens = fallback.CacheCreation1hTokens
	}
	if primary.TotalTokens == 0 {
		primary.TotalTokens = fallback.TotalTokens
	}
	return primary
}

// normalizeRawUsage fills missing TotalTokens from prompt+completion and then
// from the scalar fallback.
func normalizeRawUsage(usage rawUsage, fallback int64) rawUsage {
	return normalizeRawUsageWithFallback(usage, rawUsage{TotalTokens: fallback})
}

// normalizeRawUsageWithFallback fills missing fields from a fallback rawUsage.
//
// When the upstream omits total_tokens, the derived total follows ADR §2: the
// real billing total is the sum of all five canonical buckets. This only
// affects the token-fallback billing path (calculateCostWithUsage with no
// bucket usage); whenever any bucket is populated, calculateCostWithUsage
// uses the buckets directly and TotalTokens is not consulted.
func normalizeRawUsageWithFallback(usage rawUsage, fallback rawUsage) rawUsage {
	if usage.TotalTokens == 0 {
		derived := usage.PromptTokens + usage.CompletionTokens +
			usage.CacheReadTokens + usage.CacheCreation5mTokens + usage.CacheCreation1hTokens
		if derived > 0 {
			usage.TotalTokens = derived
		}
	}
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = fallback.TotalTokens
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = fallback.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = fallback.CompletionTokens
	}
	return usage
}

// hasRawUsage reports whether any usage field is set.
func hasRawUsage(usage rawUsage) bool {
	return usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.CacheReadTokens > 0 || usage.CacheCreation5mTokens > 0 || usage.CacheCreation1hTokens > 0
}

// cacheReadTokensFromUsageMap extracts cache-read tokens from a usage map,
// checking both flat keys and nested *_details objects. Flat keys cover
// Anthropic (cache_read_input_tokens), OpenAI-compatible relays
// (cache_read_tokens) and OpenAI Responses (cached_tokens).
func cacheReadTokensFromUsageMap(m map[string]interface{}) int64 {
	if value := numberField(m, "cache_read_input_tokens", "cache_read_tokens", "cached_tokens"); value != 0 {
		if value < 0 {
			recordTokenUsageAnomaly("negative")
			return 0
		}
		return value
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[key].(map[string]interface{})
		if !ok {
			continue
		}
		if value := numberField(details, "cache_read_tokens", "cached_tokens"); value != 0 {
			if value < 0 {
				recordTokenUsageAnomaly("negative")
				return 0
			}
			return value
		}
	}
	return 0
}

// cacheCreationDetailTokens reads the cache-creation buckets from a usage map
// per docs/design/token-usage-semantics.md §3.3 and §4.2. It returns the
// 5m and 1h TTL-split token counts, the flat cache_creation_input_tokens
// total (0 when absent), and whether ephemeral detail was present.
//
// Rules:
//   - cache_creation.ephemeral_5m_input_tokens / ephemeral_1h_input_tokens
//     are the canonical detail buckets.
//   - When only the flat cache_creation_input_tokens total is present (no
//     ephemeral detail), the whole total defaults into the 5m bucket
//     (ADR §4.2; Anthropic default cache TTL is 5m). No guessing by model
//     name.
//   - When both total and detail are present, detail wins; a detail sum that
//     exceeds the total is recorded as a ttl_detail_exceeds_total anomaly
//     (ADR §4.2). The caller does not need to recompute the excess; this
//     helper records it once.
//   - Negative values are clamped to 0 and recorded as a negative anomaly
//     (ADR §4.1).
func cacheCreationDetailTokens(m map[string]interface{}) (fiveM, oneH, flatTotal int64, hadDetail bool) {
	if raw := numberField(m, "cache_creation_input_tokens"); raw != 0 {
		if raw < 0 {
			recordTokenUsageAnomaly("negative")
		} else {
			flatTotal = raw
		}
	}
	// Provider-level flattened buckets (relayprovider.UsageTokenDetails JSON tags).
	if raw := numberField(m, "cache_creation_5m_tokens"); raw > 0 {
		hadDetail = true
		if raw < 0 {
			recordTokenUsageAnomaly("negative")
		} else {
			fiveM = raw
		}
	}
	if raw := numberField(m, "cache_creation_1h_tokens"); raw > 0 {
		hadDetail = true
		if raw < 0 {
			recordTokenUsageAnomaly("negative")
		} else {
			oneH = raw
		}
	}
	// Provider-level flattened buckets may also live inside prompt_tokens_details /
	// input_tokens_details (e.g. after apicompat Responses->Chat conversion).
	for _, detailsKey := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[detailsKey].(map[string]interface{})
		if !ok {
			continue
		}
		if raw := numberField(details, "cache_creation_5m_tokens"); raw > 0 {
			hadDetail = true
			if raw < 0 {
				recordTokenUsageAnomaly("negative")
			} else {
				fiveM = raw
			}
		}
		if raw := numberField(details, "cache_creation_1h_tokens"); raw > 0 {
			hadDetail = true
			if raw < 0 {
				recordTokenUsageAnomaly("negative")
			} else {
				oneH = raw
			}
		}
	}
	nested, _ := m["cache_creation"].(map[string]interface{})
	if nested != nil {
		if raw := numberField(nested, "ephemeral_5m_input_tokens"); raw != 0 {
			hadDetail = true
			if raw < 0 {
				recordTokenUsageAnomaly("negative")
			} else {
				fiveM = raw
			}
		}
		if raw := numberField(nested, "ephemeral_1h_input_tokens"); raw != 0 {
			hadDetail = true
			if raw < 0 {
				recordTokenUsageAnomaly("negative")
			} else {
				oneH = raw
			}
		}
	}
	if !hadDetail && flatTotal > 0 {
		// No TTL detail: default the flat total into the 5m bucket (ADR §4.2).
		fiveM = flatTotal
	}
	if hadDetail && flatTotal > 0 && fiveM+oneH > flatTotal {
		// Detail sum exceeds the flat total: detail wins (already set),
		// record the inconsistency. Billing is unchanged because detail is
		// the more precise signal.
		recordTokenUsageAnomaly("ttl_detail_exceeds_total")
	}
	return fiveM, oneH, flatTotal, hadDetail
}

// recordTokenUsageAnomaly is the single entry point for low-cardinality
// token-usage parse anomalies (ADR §4). It guards against nil metrics in
// tests that link http_raw_helpers.go without registering the collector.
func recordTokenUsageAnomaly(reason string) {
	if metrics.TokenUsageParseAnomaly != nil {
		metrics.TokenUsageParseAnomaly.WithLabelValues(reason).Inc()
	}
}

// clampNonNegInt64 returns 0 for negative inputs (ADR §4.1); otherwise the
// value unchanged.
func clampNonNegInt64(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// numberField returns the first non-zero numeric value found under any of the
// given keys in a map.
func numberField(m map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if number := int64Value(value); number != 0 {
				return number
			}
		}
	}
	return 0
}

// int64Value coerces an unmarshalled JSON numeric value to int64.
func int64Value(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case jsonx.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// parseResponsesResourcePath parses /v1/responses/<id>[/<sub>] paths into a
// responseID and a boolean indicating whether the path is a supported
// resource route.
func parseResponsesResourcePath(method, path string) (string, bool) {
	const prefix = "/v1/responses/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" || rest == path {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if parts[0] == "" {
		return "", false
	}
	switch {
	case len(parts) == 1 && (method == http.MethodGet || method == http.MethodDelete):
		return parts[0], true
	case len(parts) == 2 && parts[1] == "cancel" && method == http.MethodPost:
		return parts[0], true
	case len(parts) == 2 && parts[1] == "input_items" && method == http.MethodGet:
		return parts[0], true
	default:
		return "", false
	}
}

// defaultRawModel returns the default model for endpoints that don't require
// one in the request body (embeddings, moderations, tts).
func defaultRawModel(upstreamPath string) string {
	switch upstreamPath {
	case "/embeddings":
		return "text-embedding-ada-002"
	case "/moderations":
		return "text-moderation-latest"
	case "/audio/speech":
		return "tts-1"
	default:
		return ""
	}
}

// estimateRawPromptTokens returns a rough 1/4-char estimate of prompt tokens.
func estimateRawPromptTokens(body []byte) int64 {
	tokens := int64(len(body) / 4)
	if tokens < 1 {
		return 1
	}
	return tokens
}

// estimateRawUsage returns a rough usage estimate for a raw request body.
func estimateRawUsage(body []byte) rawUsage {
	promptTokens := estimateRawPromptTokens(body)
	completionTokens := int64(100)
	return rawUsage{
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		CacheCreation5mTokens: 0,
		CacheCreation1hTokens: 0,
		TotalTokens:           promptTokens + completionTokens,
	}
}

// estimateRawTokens returns the estimated total tokens for a raw body.
func estimateRawTokens(body []byte) int64 {
	return estimateRawUsage(body).TotalTokens
}

// extractTotalTokens returns the total tokens from a body, falling back to an
// estimate when the body carries no usage.
func extractTotalTokens(body []byte, fallback int64) int64 {
	return extractRawUsage(body, fallback).TotalTokens
}

// writeRawResponse writes a non-streaming raw upstream response to the
// client, filtering hop-by-hop and Content-Type headers.
func writeRawResponse(w http.ResponseWriter, resp *relayprovider.RawResponse) {
	for key, values := range resp.Header {
		if isRelayHopByHopHeader(key) || strings.EqualFold(key, "Content-Type") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Type", safeRawContentType(resp.Header.Get("Content-Type"), "application/json"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body) // #nosec G705 -- upstream content type is constrained and nosniff is set above.
}

// safeRawContentType validates and constrains an upstream Content-Type to a
// safe, non-executable media type.
func safeRawContentType(contentType, fallback string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return fallback
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "application/octet-stream"
	}
	mediaType = strings.ToLower(mediaType)
	switch {
	case mediaType == "application/json",
		mediaType == "application/x-ndjson",
		mediaType == "application/octet-stream",
		mediaType == "text/event-stream",
		strings.HasSuffix(mediaType, "+json"):
		return contentType
	default:
		return "application/octet-stream"
	}
}

// rawStreamUsageTracker observes SSE chunks from a raw stream and accumulates
// usage + response-id information.
type rawStreamUsageTracker struct {
	fallback   rawUsage
	usage      rawUsage
	responseID string
	pending    string
}

// newRawStreamUsageTracker creates a tracker seeded with a fallback usage.
func newRawStreamUsageTracker(fallback rawUsage) *rawStreamUsageTracker {
	return &rawStreamUsageTracker{fallback: fallback}
}

// Observe parses a complete data payload (without the "data: " prefix) and
// updates accumulated usage / response-id.
func (t *rawStreamUsageTracker) Observe(chunk []byte) {
	if t.responseID == "" {
		t.responseID = extractRawStreamResponseID(chunk)
	}
	usage := extractRawUsage(chunk, 0)
	if hasRawUsage(usage) {
		// Per-chunk total_tokens is unreliable for the Anthropic
		// message_start/message_delta split (start reports input-side total,
		// delta reports output-side total; neither is the full five-bucket
		// sum per ADR §2). Drop it here and let normalizeRawUsageWithFallback
		// derive the real total from the accumulated buckets at Usage() time.
		usage.TotalTokens = 0
		t.usage = mergeRawUsage(usage, t.usage)
	}
}

// ObserveBytes consumes raw stream bytes, splitting on newlines into data
// payloads that are forwarded to Observe.
func (t *rawStreamUsageTracker) ObserveBytes(p []byte) {
	t.pending += string(p)
	for {
		line, rest, ok := strings.Cut(t.pending, "\n")
		if !ok {
			break
		}
		t.pending = rest
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if !ok || data == "" || data == "[DONE]" {
			continue
		}
		t.Observe([]byte(data))
	}
}

// Usage returns the accumulated usage, flushing any pending partial line.
func (t *rawStreamUsageTracker) Usage() rawUsage {
	if strings.TrimSpace(t.pending) != "" {
		t.ObserveBytes([]byte("\n"))
	}
	return normalizeRawUsageWithFallback(t.usage, t.fallback)
}

// ResponseID returns the response id observed so far, flushing any pending
// partial line.
func (t *rawStreamUsageTracker) ResponseID() string {
	if strings.TrimSpace(t.pending) != "" {
		t.ObserveBytes([]byte("\n"))
	}
	return t.responseID
}

// extractRawStreamResponseID pulls a response id from a raw stream chunk.
func extractRawStreamResponseID(chunk []byte) string {
	var payload interface{}
	if err := jsonx.Unmarshal(chunk, &payload); err != nil {
		return ""
	}
	return extractRawStreamResponseIDValue(payload)
}

// extractRawStreamResponseIDValue searches an unmarshalled value for a
// response id in the shapes emitted by the Responses API.
func extractRawStreamResponseIDValue(value interface{}) string {
	typed, ok := value.(map[string]interface{})
	if !ok {
		return ""
	}
	if responseID, _ := typed["response_id"].(string); strings.TrimSpace(responseID) != "" {
		return strings.TrimSpace(responseID)
	}
	if response, ok := typed["response"].(map[string]interface{}); ok {
		if responseID, _ := response["id"].(string); strings.TrimSpace(responseID) != "" {
			return strings.TrimSpace(responseID)
		}
	}
	if object, _ := typed["object"].(string); object == "response" {
		if responseID, _ := typed["id"].(string); strings.TrimSpace(responseID) != "" {
			return strings.TrimSpace(responseID)
		}
	}
	return ""
}

// writeRawStreamResponse writes a streaming raw upstream response to the
// client, optionally tracking usage via the supplied trackers.
func writeRawStreamResponse(w http.ResponseWriter, resp *relayprovider.RawStreamResponse, usageTracker ...*rawStreamUsageTracker) {
	defer resp.Body.Close()

	for key, values := range resp.Header {
		if isRelayHopByHopHeader(key) || strings.EqualFold(key, "Content-Type") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Type", safeRawContentType(resp.Header.Get("Content-Type"), "text/event-stream"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	var copyErr error
	if flusher, ok := w.(http.Flusher); ok {
		_, copyErr = io.Copy(&flushWriter{w: w, flusher: flusher, usageTracker: firstRawStreamUsageTracker(usageTracker)}, resp.Body)
	} else {
		_, copyErr = io.Copy(&streamUsageWriter{w: w, usageTracker: firstRawStreamUsageTracker(usageTracker)}, resp.Body)
	}
	if errors.Is(copyErr, relayprovider.ErrStreamIdleTimeout) {
		applogger.Log.Warn("upstream SSE stream closed after idle timeout", zap.Error(copyErr))
	}
}

// firstRawStreamUsageTracker returns the first tracker in a variadic list, or
// nil when none are supplied.
func firstRawStreamUsageTracker(trackers []*rawStreamUsageTracker) *rawStreamUsageTracker {
	if len(trackers) == 0 {
		return nil
	}
	return trackers[0]
}

// flushWriter wraps an http.ResponseWriter that supports flushing, flushing
// after every Write and optionally observing stream usage.
type flushWriter struct {
	w            http.ResponseWriter
	flusher      http.Flusher
	usageTracker *rawStreamUsageTracker
}

// Write implements io.Writer.
func (w *flushWriter) Write(p []byte) (int, error) {
	observeStreamUsage(w.usageTracker, p)
	n, err := w.w.Write(p)
	w.flusher.Flush()
	return n, err
}

// streamUsageWriter wraps an io.Writer, observing stream usage on Write.
type streamUsageWriter struct {
	w            io.Writer
	usageTracker *rawStreamUsageTracker
}

// Write implements io.Writer.
func (w *streamUsageWriter) Write(p []byte) (int, error) {
	observeStreamUsage(w.usageTracker, p)
	return w.w.Write(p)
}

// observeStreamUsage forwards bytes to a tracker if one is present.
func observeStreamUsage(tracker *rawStreamUsageTracker, p []byte) {
	if tracker == nil {
		return
	}
	tracker.ObserveBytes(p)
}

// isRelayHopByHopHeader reports whether a header is a hop-by-hop header that
// must not be forwarded between upstream and client.
func isRelayHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

// parsePositiveInt64 parses a positive int64, returning an error otherwise.
func parsePositiveInt64(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("id must be positive")
	}
	return id, nil
}

// generateRequestID returns a random hex request id with a "req_" prefix,
// falling back to a timestamp-based id if the CSPRNG fails.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("req_%x", b)
}
