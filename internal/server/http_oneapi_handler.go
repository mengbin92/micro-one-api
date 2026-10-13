package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
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

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if err := s.checkUserRPM(r.Context(), authSnapshot.UserId); err != nil {
		s.writeUserRPMError(w)
		return
	}

	channelReply, err := s.channelClient.GetChannel(r.Context(), &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		s.handleChannelError(w, err)
		return
	}
	if channelReply.Channel == nil {
		s.writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	body, err := readRouteRequestBody(r)
	if err != nil {
		s.writeRequestBodyError(w, r, err)
		return
	}
	clientModel := extractRawModel(body)
	ch := &relaybiz.Channel{
		ID: channelReply.Channel.Id, Type: channelReply.Channel.Type, BaseURL: channelReply.Channel.BaseUrl,
		Key: channelReply.Channel.Key, Models: strings.Split(channelReply.Channel.Models, ","), ModelMapping: channelReply.Channel.ModelMapping,
		UpstreamModelID: channelReply.Channel.UpstreamModelId, UpstreamConfigurationRevision: channelReply.Channel.GetUpstreamConfigurationRevision(),
		Config: relaybiz.ChannelConfig{APIVersion: channelReply.Channel.GetConfig().GetApiVersion()},
	}
	model := clientModel
	if clientModel == "" {
		model = "proxy"
	} else {
		model = relaybiz.ResolveChannelModel(ch, clientModel)
		body = rewriteRawModel(body, model)
		ch.UpstreamModelID = model
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
	billingClientModel := clientModel
	if billingClientModel == "" {
		billingClientModel = model
	}
	billingModel := s.BillingModelName(billingClientModel, model, model)
	reservation, err := s.reserveAuthenticatedQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		requestID,
		estimateRawTokens(body),
		billingModel,
		fmt.Sprintf("%d", channelReply.Channel.Id),
		0,
		authSnapshot,
		0,
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	provider, err := s.providerFactory.CreateProviderWithConfig(channelReply.Channel.Type, channelReply.Channel.BaseUrl, channelReply.Channel.Key, providerConfigFromChannelInfo(channelReply.Channel))
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
	shadowCtx := withNativeUsageShadow(r.Context(), ch, requestID, ch.UpstreamModelID)
	resp, err := provider.Forward(shadowCtx, &relayprovider.RawRequest{
		Method: r.Method,
		Path:   "/" + targetPart,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   body,
	})
	healthErr := err
	if healthErr == nil && (clientModel == "" || strings.HasPrefix(strings.Trim(targetPart, "/"), "models")) {
		healthErr = fmt.Errorf("diagnostic upstream response does not verify inference recovery")
	}
	s.relayUsecase.RecordUpstreamResult(r.Context(), ch, healthErr, time.Since(startedAt).Milliseconds(), startedAt)
	healthReported = true
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	totalTokens := extractTotalTokens(resp.Body, estimateRawTokens(body))
	usage := extractRawUsage(resp.Body, totalTokens)
	logInput := usageLogInput{
		UserID:                authSnapshot.UserId,
		TokenID:               authSnapshot.TokenId,
		TokenName:             authSnapshot.TokenName,
		RequestID:             requestID,
		Endpoint:              "/" + targetPart,
		ModelName:             billingModel,
		Quota:                 totalTokens,
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		ChannelID:             channelReply.Channel.Id,
		SubscriptionAccountID: 0,
		UpstreamModelID:       channelReply.Channel.UpstreamModelId,
		SourceKind:            relaybiz.UpstreamSourceChannel,
		ElapsedTime:           time.Since(startedAt).Milliseconds(),
		IsStream:              false,
		// v0.11.0 Phase 0/1 ADR §3.3: Anthropic-compatible channels use
		// mutually-exclusive token buckets.
		PromptExclusive: isPromptExclusiveChannelType(channelReply.Channel.Type),
	}
	logInput.applyEnvelope(envelopeFromRawUsage(usage))
	compareNativeUsageShadow(shadowCtx, logInput.Usage)
	if err := s.commitQuota(r.Context(), reservation.ReservationId, totalTokens, true, logInput); err != nil {
		s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
		return
	}
	writeRawResponse(w, resp)
}
