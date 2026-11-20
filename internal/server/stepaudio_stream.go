package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func stepAudioChannel(channel *relaybiz.Channel) bool {
	base, err := url.Parse(channel.BaseURL)
	return err == nil && channel.Type == relayprovider.ChannelTypeOpenAI &&
		base.Scheme == "https" && base.Host == "api.stepfun.com" && base.User == nil && base.RawQuery == "" && base.Fragment == "" &&
		strings.TrimRight(base.Path, "/") == "/step_plan/v1"
}

func stepAudioSpeechSuccess(resp *relayprovider.RawResponse) bool {
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || len(resp.Body) == 0 {
		return false
	}
	if strings.HasPrefix(kind, "audio/") {
		return true
	}
	var response struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	return kind == "application/json" && jsonx.Unmarshal(resp.Body, &response) == nil && strings.HasPrefix(response.Data.URL, "https://")
}

func (s *HTTPServer) forwardStepAudioSSE(ctx context.Context, w http.ResponseWriter, provider relayprovider.Provider, req *relayprovider.RawRequest, terminal string, reservation *billingv1.ReserveQuotaResponse, log usageLogInput, started time.Time) (bool, error) {
	controller := http.NewResponseController(w)
	return s.relayStepAudioSSE(ctx, provider, req, terminal, reservation, log, started, func() {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
	}, func(event string, _, frame []byte) error {
		if _, err := w.Write(frame); err != nil {
			return err
		}
		if event == "speech.audio.done" {
			if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
				return err
			}
		}
		return controller.Flush()
	})
}

func (s *HTTPServer) relayStepAudioSSE(ctx context.Context, provider relayprovider.Provider, req *relayprovider.RawRequest, terminal string, reservation *billingv1.ReserveQuotaResponse, log usageLogInput, started time.Time, onStart func(), onFrame func(string, []byte, []byte) error) (bool, error) {
	resp, err := provider.ForwardStream(ctx, req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "text/event-stream" {
		return false, relaybiz.MarkUnverifiedResponseError(fmt.Errorf("upstream audio stream is not SSE"))
	}

	if onStart != nil {
		onStart()
	}

	log.applyReservation(reservation)
	releaseReason := "audio stream ended without completion"
	defer func() {
		if releaseReason != "" {
			_ = s.releaseQuota(ctx, reservation.ReservationId, releaseReason)
		}
	}()

	const maxEventBytes = 2 << 20
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	var frame, data bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		if frame.Len()+len(line)+1 > maxEventBytes {
			return true, relaybiz.MarkUnverifiedResponseError(fmt.Errorf("upstream audio SSE event exceeds %d bytes", maxEventBytes))
		}
		frame.Write(line)
		frame.WriteByte('\n')
		if len(line) != 0 {
			if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				data.Write(bytes.TrimPrefix(value, []byte(" ")))
				data.WriteByte('\n')
			}
			continue
		}

		var event struct {
			Type string `json:"type"`
		}
		payload := bytes.TrimSpace(data.Bytes())
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		if jsonx.Unmarshal(payload, &event) == nil && event.Type == terminal {
			if terminal == "transcript.text.done" {
				var fields map[string]jsonx.RawMessage
				var text *string
				if jsonx.ValidateUnique(payload) != nil || jsonx.Unmarshal(payload, &fields) != nil ||
					jsonx.Unmarshal(fields["text"], &text) != nil || text == nil {
					return true, relaybiz.MarkUnverifiedResponseError(fmt.Errorf("invalid upstream transcription terminal"))
				}
			}
			// A complete upstream terminal proves metered work. If commit is
			// uncertain, leave recovery to Billing rather than releasing it.
			releaseReason = ""
			u := extractRawUsage(payload, 0)
			log.Quota, log.PromptTokens, log.CompletionTokens = u.TotalTokens, u.PromptTokens, u.CompletionTokens
			log.ElapsedTime = time.Since(started).Milliseconds()
			if err := s.commitQuota(ctx, reservation.ReservationId, u.TotalTokens, true, log); err != nil {
				return true, relaybiz.MarkPostForwardError(err)
			}
			s.ingestUsageLogAfterResponse(log, ctx)
		}
		writeErr := onFrame(event.Type, payload, frame.Bytes())
		switch {
		case releaseReason == "":
			return true, relaybiz.MarkPostForwardError(writeErr)
		case event.Type == "error" || event.Type == "speech.audio.error":
			releaseReason = "upstream audio stream failed"
			return true, relaybiz.MarkUnverifiedResponseError(fmt.Errorf("upstream audio stream failed"))
		case writeErr != nil:
			releaseReason = "audio response canceled"
			return true, relaybiz.MarkUnverifiedResponseError(writeErr)
		}
		frame.Reset()
		data.Reset()
	}
	if err := scanner.Err(); err != nil {
		return true, relaybiz.MarkUnverifiedResponseError(fmt.Errorf("read upstream audio stream: %w", err))
	}
	return true, relaybiz.MarkUnverifiedResponseError(fmt.Errorf("audio stream ended without completion"))
}
