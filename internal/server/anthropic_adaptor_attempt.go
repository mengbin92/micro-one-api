package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/adaptor"
	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
)

// executeAnthropicChannelAttempt executes one API-key channel attempt through
// the unified adaptor layer. Channel retry/fallback remains owned by the
// caller's RetryExecutor; this function owns one reservation and one upstream
// response only.
func (s *HTTPServer) executeAnthropicChannelAttempt(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	plan *relaybiz.RelayPlan,
	channel *relaybiz.Channel,
	request *apicompat.AnthropicRequest,
	originalBody []byte,
	clientModel string,
	resolvedModel string,
) error {
	startedAt := time.Now()
	requestID := generateRequestID()
	billingModel := s.BillingModelName(clientModel, plan.ResolvedModel, resolvedModel)

	relayContext := &adaptor.RelayContext{
		InboundFormat: adaptor.FormatAnthropicMessages,
		ClientModel:   clientModel,
		ResolvedModel: resolvedModel,
		Channel:       channel,
		IsStream:      request.Stream,
		UserID:        plan.Auth.UserID,
		RequestID:     requestID,
		RawBody:       originalBody,
		InboundHeader: r.Header.Clone(),
	}

	channelAdaptor, ok := adaptor.GetAdaptor(channel.Type)
	if !ok {
		return fmt.Errorf("no adaptor registered for channel type %d", channel.Type)
	}
	channelAdaptor.Init(relayContext)
	upstreamFormat, upstreamBody, err := channelAdaptor.ConvertRequest(relayContext, adaptor.FormatAnthropicMessages, originalBody)
	if err != nil {
		return fmt.Errorf("adaptor convert request: %w", err)
	}
	upstreamRequest, err := channelAdaptor.BuildUpstreamRequest(ctx, relayContext, upstreamFormat, upstreamBody)
	if err != nil {
		return fmt.Errorf("adaptor build request: %w", err)
	}
	if err := provider.ValidateBaseURLForChannel(channel.Type, upstreamRequest.URL.String()); err != nil {
		return fmt.Errorf("validate upstream URL: %w", err)
	}

	estimatedUsage := estimateRawUsage(upstreamBody)
	reservation, err := s.reserveQuota(
		ctx,
		fmt.Sprintf("%d", plan.Auth.UserID),
		requestID,
		estimatedUsage.TotalTokens,
		billingModel,
		fmt.Sprintf("%d", channel.ID),
		subscriptionAccountIDFromPlan(plan),
	)
	if err != nil {
		return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: err}
	}

	client := s.apiKeyHTTPClient
	if request.Stream {
		client = s.apiKeyStreamHTTPClient
	}
	if client == nil {
		if request.Stream {
			client = provider.NewStreamHTTPClient(30 * time.Second)
		} else {
			client = provider.NewHTTPClient(30 * time.Second)
		}
	}

	response, err := client.Do(upstreamRequest) // #nosec G704 -- adaptor URL validated above.
	if err != nil {
		_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
		return fmt.Errorf("upstream call: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, provider.MaxUpstreamErrorBody))
		_ = response.Body.Close()
		_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
		return &provider.UpstreamHTTPError{StatusCode: response.StatusCode, Body: body}
	}

	logInput := usageLogInput{
		UserID:                plan.Auth.UserID,
		TokenID:               plan.Auth.TokenID,
		TokenName:             plan.Auth.TokenName,
		RequestID:             requestID,
		Endpoint:              "/v1/messages",
		ModelName:             billingModel,
		ChannelID:             channel.ID,
		SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
		IsStream:              request.Stream,
	}
	logInput.applyChannelInputs(channel)
	logInput.PromptExclusive = isPromptExclusiveChannelType(channel.Type)

	if request.Stream {
		return s.writeAnthropicAdaptorStream(ctx, w, response, channelAdaptor, relayContext, upstreamFormat, reservation.ReservationId, estimatedUsage, logInput, startedAt)
	}
	return s.writeAnthropicAdaptorResponse(ctx, w, response, channelAdaptor, relayContext, upstreamFormat, reservation.ReservationId, estimatedUsage, logInput, startedAt)
}

func (s *HTTPServer) writeAnthropicAdaptorResponse(
	ctx context.Context,
	w http.ResponseWriter,
	response *http.Response,
	channelAdaptor adaptor.Adaptor,
	relayContext *adaptor.RelayContext,
	upstreamFormat adaptor.Format,
	reservationID string,
	estimatedUsage rawUsage,
	logInput usageLogInput,
	startedAt time.Time,
) error {
	rawUpstream, err := io.ReadAll(io.LimitReader(response.Body, provider.MaxUpstreamResponseBody))
	_ = response.Body.Close()
	if err != nil {
		_ = s.releaseQuota(ctx, reservationID, "read upstream response error")
		return fmt.Errorf("read upstream response: %w", err)
	}
	response.Body = io.NopCloser(bytes.NewReader(rawUpstream))
	_, outputBody, err := channelAdaptor.ConvertResponse(relayContext, upstreamFormat, response)
	if err != nil {
		_ = s.releaseQuota(ctx, reservationID, "adaptor convert response error")
		return err
	}

	usage := extractRawUsage(rawUpstream, estimatedUsage.TotalTokens)
	populateAnthropicUsageLog(&logInput, usage, time.Since(startedAt))
	if err := s.commitQuota(ctx, reservationID, usage.TotalTokens, true, logInput); err != nil {
		return err
	}
	logUpstreamUsage(logInput)
	s.ingestUsageLog(ctx, logInput)

	copyAnthropicUpstreamHeaders(w.Header(), response.Header)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(outputBody) // #nosec G705 -- adaptor output is JSON and nosniff is set.
	return nil
}

func (s *HTTPServer) writeAnthropicAdaptorStream(
	ctx context.Context,
	w http.ResponseWriter,
	response *http.Response,
	channelAdaptor adaptor.Adaptor,
	relayContext *adaptor.RelayContext,
	upstreamFormat adaptor.Format,
	reservationID string,
	estimatedUsage rawUsage,
	logInput usageLogInput,
	startedAt time.Time,
) error {
	upstreamUsage := newRawStreamUsageTracker(estimatedUsage)
	originalBody := response.Body
	response.Body = &observedReadCloser{
		Reader: io.TeeReader(originalBody, &streamUsageWriter{w: io.Discard, usageTracker: upstreamUsage}),
		Closer: originalBody,
	}

	_, reader, err := channelAdaptor.ConvertStreamResponse(relayContext, upstreamFormat, response)
	if err != nil {
		_ = response.Body.Close()
		_ = s.releaseQuota(ctx, reservationID, "adaptor convert stream error")
		return err
	}
	firstEvent, reader, err := preflightAnthropicStream(reader)
	if err != nil {
		if closer, ok := reader.(io.Closer); ok {
			_ = closer.Close()
		}
		_ = response.Body.Close()
		_ = s.releaseQuota(ctx, reservationID, "upstream stream failed before first event")
		return fmt.Errorf("preflight upstream stream: %w", err)
	}

	copyAnthropicUpstreamHeaders(w.Header(), response.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		output := &flushWriter{w: w, flusher: flusher}
		_, _ = output.Write(firstEvent)
		_, _ = io.Copy(output, reader)
	} else {
		_, _ = w.Write(firstEvent) // #nosec G705 -- SSE bytes are opaque protocol data, not HTML interpolation.
		_, _ = io.Copy(w, reader)
	}
	if closer, ok := reader.(io.Closer); ok {
		_ = closer.Close()
	}
	_ = response.Body.Close()

	usage := upstreamUsage.Usage()
	populateAnthropicUsageLog(&logInput, usage, time.Since(startedAt))
	if err := s.commitQuotaAfterResponseObserved(ctx, reservationID, usage.TotalTokens, true, logInput); err != nil {
		s.logPostResponseCommitError(err)
	} else {
		logUpstreamUsage(logInput)
		s.ingestUsageLogAfterResponse(logInput)
	}
	return nil
}

func populateAnthropicUsageLog(input *usageLogInput, usage rawUsage, elapsed time.Duration) {
	input.Quota = usage.TotalTokens
	input.PromptTokens = usage.PromptTokens
	input.CompletionTokens = usage.CompletionTokens
	input.CacheReadTokens = usage.CacheReadTokens
	input.CacheCreation5mTokens = usage.CacheCreation5mTokens
	input.CacheCreation1hTokens = usage.CacheCreation1hTokens
	input.ElapsedTime = elapsed.Milliseconds()
	// §4.2: the semantics verdict comes from the accumulated field shape of
	// the raw upstream response (anthropic_messages markers for Anthropic
	// channels), not from the channel type.
	input.applyEnvelope(envelopeFromRawUsage(usage))
}

type observedReadCloser struct {
	io.Reader
	io.Closer
}

func copyAnthropicUpstreamHeaders(destination, source http.Header) {
	for key, values := range source {
		if isRelayHopByHopHeader(key) || IsRelayCORSResponseHeader(key) || strings.EqualFold(key, "Content-Type") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func preflightAnthropicStream(reader io.Reader) ([]byte, io.Reader, error) {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	stream := &bufferedStreamReader{Reader: buffered}
	if closer, ok := reader.(io.Closer); ok {
		stream.closer = closer
	}
	var first bytes.Buffer
	for first.Len() <= 4*1024*1024 {
		line, err := buffered.ReadString('\n')
		if len(line) > 0 {
			_, _ = first.WriteString(line)
			if strings.TrimSpace(line) == "" {
				payload := first.Bytes()
				if strings.Contains(string(payload), "event: error") || strings.Contains(string(payload), `"type":"error"`) {
					return nil, stream, fmt.Errorf("upstream emitted an error before the first response event")
				}
				return bytes.Clone(payload), stream, nil
			}
		}
		if err != nil {
			return nil, stream, fmt.Errorf("stream ended before a complete SSE event: %w", err)
		}
	}
	return nil, stream, fmt.Errorf("first SSE event exceeds 4 MiB")
}

type bufferedStreamReader struct {
	*bufio.Reader
	closer io.Closer
}

func (reader *bufferedStreamReader) Close() error {
	if reader.closer == nil {
		return nil
	}
	return reader.closer.Close()
}
