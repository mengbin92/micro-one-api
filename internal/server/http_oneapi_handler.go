package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func (s *HTTPServer) handleOneAPIProxy(w http.ResponseWriter, r *http.Request) {
	const prefix = "/v1/oneapi/proxy/"

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, prefix)
	channelPart, targetPart, ok := strings.Cut(rest, "/")
	if !ok || channelPart == "" || targetPart == "" {
		s.writeError(w, http.StatusBadRequest, "invalid proxy path")
		return
	}
	channelID, err := parsePositiveInt64(channelPart)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	targetPart = strings.TrimPrefix(targetPart, "v1/")
	switch targetPart {
	case "chat/completions", "messages", "completions", "embeddings", "moderations", "rerank", "images/generations", "audio/speech":
	default:
		s.handleUnsupportedOpenAIRoute("oneapi.proxy")(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		s.writeError(w, http.StatusBadRequest, "query parameters are not supported")
		return
	}

	snapshot, err := s.getRawAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if !snapshot.GetUserEnabled() || !snapshot.GetTokenEnabled() {
		s.writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	if err := s.checkUserRPM(r.Context(), snapshot.UserId); err != nil {
		s.writeUserRPMError(w)
		return
	}

	body, err := readRouteRequestBody(r)
	if err != nil {
		s.writeRequestBodyError(w, r, err)
		return
	}
	var payload map[string]jsonx.RawMessage
	var clientModel string
	if jsonx.Unmarshal(body, &payload) != nil || jsonx.Unmarshal(payload["model"], &clientModel) != nil || strings.TrimSpace(clientModel) == "" {
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	clientModel = relaybiz.RelayModelName(strings.TrimSpace(clientModel))
	if clientModel == "" || !authAllowsModel(snapshot.AllowedModels, clientModel) {
		s.writeError(w, http.StatusForbidden, "model not allowed")
		return
	}
	auth, err := s.routingAuth(r.Context(), snapshot, 0)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	permission, err := s.checkStoredSource(r.Context(), auth.Group, clientModel, routing.Source{Kind: routing.Channel, ID: channelID})
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "routing authority unavailable")
		return
	}
	if !permission.Allowed {
		s.writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	channelReply, err := s.channelClient.GetChannel(r.Context(), &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		s.handleChannelError(w, err)
		return
	}
	channel := channelReply.GetChannel()
	if channel == nil || channel.Id != channelID || channel.Status != 1 || isSubscriptionChannel(channel.Type) {
		s.writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	ch := &relaybiz.Channel{
		ID: channel.Id, Type: channel.Type, BaseURL: channel.BaseUrl,
		Key: channel.Key, Models: strings.Split(channel.Models, ","), ModelMapping: channel.ModelMapping,
		UpstreamModelID: permission.UpstreamModelID, UpstreamConfigurationRevision: channel.GetUpstreamConfigurationRevision(),
	}
	model := clientModel
	if s.relayUsecase != nil {
		model = s.relayUsecase.ResolveModel(clientModel)
	}
	model = relaybiz.ResolveChannelModel(ch, model)
	ch.UpstreamModelID = model
	// Serialize a single model selector for providers that decode JSON keys differently.
	for field := range payload {
		if strings.EqualFold(field, "model") {
			delete(payload, field)
		}
	}
	payload["model"], _ = jsonx.Marshal(model)
	body, err = jsonx.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ch, err = s.relayUsecase.AdmitUpstreamExecution(r.Context(), ch)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "channel unavailable")
		return
	}
	healthReported := false
	defer func() {
		if !healthReported && ch.HealthProbeID != "" {
			s.relayUsecase.RecordUpstreamResult(r.Context(), ch, fmt.Errorf("upstream attempt was not dispatched"), 0, time.Now())
		}
	}()

	requestID := generateRequestID()
	r = r.WithContext(channelAttemptContext(r.Context(), requestID, 1, ch, ch.UpstreamModelID))
	startedAt := time.Now()
	billingModel := s.BillingModelName(clientModel, model, model)
	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", auth.UserID),
		requestID,
		estimateRawTokens(body),
		billingModel,
		fmt.Sprintf("%d", channelID),
		0,
		auth.RoutingContext,
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, providerConfigFromChannelInfo(channel))
	if err != nil {
		s.relayUsecase.RecordUpstreamResult(r.Context(), ch, relayprovider.BeforeForward(err), time.Since(startedAt).Milliseconds(), startedAt)
		healthReported = true
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "failed to create provider")
		s.writeError(w, http.StatusInternalServerError, "failed to create provider")
		return
	}

	releaseChannel, slotErr := s.relayUsecase.AcquireChannelSlot(r.Context(), ch)
	if slotErr != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "channel slot admission rejected")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(slotErr)), "channel unavailable")
		return
	}
	defer releaseChannel()
	shadowCtx := s.observeNativeUsage(r.Context(), ch, requestID, ch.UpstreamModelID)
	headers := http.Header(relayExecutorHeaders(r.Header))
	headers.Set("Content-Type", "application/json")
	resp, err := provider.Forward(shadowCtx, &relayprovider.RawRequest{
		Method: r.Method,
		Path:   "/" + targetPart,
		Header: headers,
		Body:   body,
	})
	s.relayUsecase.RecordUpstreamResult(r.Context(), ch, err, time.Since(startedAt).Milliseconds(), startedAt)
	healthReported = true
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	totalTokens := extractTotalTokens(resp.Body, estimateRawTokens(body))
	usage := extractRawUsage(resp.Body, totalTokens)
	logInput := usageLogInput{
		UserID:                auth.UserID,
		TokenID:               auth.TokenID,
		TokenName:             auth.TokenName,
		RequestID:             requestID,
		Endpoint:              "/" + targetPart,
		ModelName:             billingModel,
		Quota:                 totalTokens,
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		ChannelID:             channelID,
		SubscriptionAccountID: 0,
		UpstreamModelID:       model,
		SourceKind:            relaybiz.UpstreamSourceChannel,
		ElapsedTime:           time.Since(startedAt).Milliseconds(),
		IsStream:              false,
		// v0.11.0 Phase 0/1 ADR §3.3: Anthropic-compatible channels use
		// mutually-exclusive token buckets.
		PromptExclusive: isPromptExclusiveChannelType(ch.Type),
	}
	logInput.applyEnvelope(envelopeFromRawUsage(usage))
	compareNativeUsageShadow(shadowCtx, logInput.Usage)
	if err := s.commitQuota(r.Context(), reservation.ReservationId, totalTokens, true, logInput); err != nil {
		s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
		return
	}
	writeRawResponse(w, resp)
}
