package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	billingdomain "micro-one-api/domain/billing"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/gemini"
	"micro-one-api/pkg/jsonx"
)

func (s *HTTPServer) handleGemini(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeGeminiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	model, method, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":")
	if !ok || !gemini.ValidModel(model) || (method != "generateContent" && method != "streamGenerateContent") || strings.Contains(r.URL.EscapedPath(), "%") {
		writeGeminiError(w, http.StatusNotImplemented, "only native text generateContent and streamGenerateContent are supported")
		return
	}
	stream := method == "streamGenerateContent"
	setRelayObservationStream(r.Context(), stream)
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "invalid query parameters")
		return
	}
	for key, values := range query {
		if len(values) != 1 || (key != "key" && key != "alt") || key == "alt" && (values[0] != "sse" || !stream) {
			writeGeminiError(w, http.StatusBadRequest, "unsupported or duplicate Gemini query parameter")
			return
		}
	}
	token, err := geminiToken(r, query)
	if err != nil {
		writeGeminiError(w, http.StatusUnauthorized, err.Error())
		return
	}
	body, err := readRequestBody(r, gemini.MaxRequestBytes)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errRequestBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeGeminiError(w, status, "invalid or oversized request body")
		return
	}
	body, _, err = gemini.PrepareRequest(body)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, err.Error())
		return
	}
	applyRequestBudget(r, []byte(`{"stream":`+strconv.FormatBool(stream)+`}`))
	tier, _ := readServiceTierRequest(body, "gemini")
	*r = *r.WithContext(billingdomain.WithServiceTierRequest(r.Context(), tier))
	if s.billingClient == nil {
		writeGeminiError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}
	result, err := s.newStagedRelayExecutor().Execute(r.Context(), relaybiz.ExecutorRequest{
		ClientIP: relayClientIP(r),
		Token:    token, Model: model, Endpoint: string(EndpointGemini), Body: body,
		Headers: relayExecutorHeaders(r.Header), RequestID: rootRequestID(r),
		SessionHash: extractSessionHashFromRequest(r, body), Stream: stream,
	})
	if err != nil {
		status := result.StatusCode
		if status == 0 {
			status = http.StatusBadGateway
		}
		setErrorSource(w, result.ChannelID, result.SubscriptionAccountID)
		writeGeminiError(w, status, orchestratorErrorMessage(status, err))
		return
	}
	// Supplier content types cannot turn a native response into executable HTML.
	if stream {
		result.Headers["Content-Type"] = []string{"text/event-stream"}
		writeOrchestratedRelayStream(w, result)
		return
	}
	result.Headers["Content-Type"] = []string{"application/json"}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeOrchestratedRelayResult(w, relayResultFromExecutionResponse(result))
}

func writeGeminiError(w http.ResponseWriter, code int, message string) {
	status := "INTERNAL"
	switch code {
	case 400, 413:
		status = "INVALID_ARGUMENT"
	case 401:
		status = "UNAUTHENTICATED"
	case 402, 429:
		status = "RESOURCE_EXHAUSTED"
	case 403:
		status = "PERMISSION_DENIED"
	case 404:
		status = "NOT_FOUND"
	case 405, 501:
		status = "UNIMPLEMENTED"
	case 503:
		status = "UNAVAILABLE"
	case 504:
		status = "DEADLINE_EXCEEDED"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = jsonx.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "status": status, "message": message}})
}

// Normalize credentials before shared middleware so token-scoped checks use the
// same identity for headers and query authentication. The query key is removed
// before audit/trace helpers run. Native response caching is explicitly outside
// B2; incomplete SSE must never enter the generic HTTP idempotency cache.
func prepareGeminiInbound(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if r.Method != http.MethodPost {
		return r, true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeGeminiError(w, http.StatusBadRequest, "Idempotency-Key is unsupported for native Gemini; billing settlement remains idempotent")
		return r, false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "invalid query parameters")
		return r, false
	}
	if values, exists := query["key"]; exists && len(values) != 1 {
		writeGeminiError(w, http.StatusBadRequest, "duplicate API key query parameter")
		return r, false
	}
	token, err := geminiToken(r, query)
	if err != nil {
		writeGeminiError(w, http.StatusUnauthorized, err.Error())
		return r, false
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Del("x-goog-api-key")
	r.Header.Del("x-api-key")
	query.Del("key")
	r.URL.RawQuery = query.Encode()
	r.RequestURI = r.URL.RequestURI()
	return r, true
}

func geminiToken(r *http.Request, query url.Values) (string, error) {
	// Native SDKs use x-goog-api-key; REST clients may use ?key=. Conflicting
	// credentials fail closed. Neither headers nor query credentials are sent
	// to the executor; the selected channel owns the supplier credential.
	token := ""
	for _, header := range []string{"x-goog-api-key", "x-api-key", "Authorization"} {
		if len(r.Header.Values(header)) > 1 {
			return "", errors.New("duplicate API key headers")
		}
	}
	bearer := ""
	if auth := r.Header.Get("Authorization"); auth != "" {
		var ok bool
		bearer, ok = strings.CutPrefix(auth, "Bearer ")
		if !ok || strings.TrimSpace(bearer) == "" {
			return "", errors.New("invalid authorization")
		}
	}
	for _, candidate := range []string{r.Header.Get("x-goog-api-key"), r.Header.Get("x-api-key"), bearer, query.Get("key")} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if token != "" && token != candidate {
			return "", errors.New("conflicting API keys")
		}
		token = candidate
	}
	if token == "" {
		return "", errors.New("missing API key")
	}
	return token, nil
}
