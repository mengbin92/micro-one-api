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

func (s *HTTPServer) forwardStepAudioSSE(ctx context.Context, w http.ResponseWriter, provider relayprovider.Provider, req *relayprovider.RawRequest, terminal, reservationID string, log usageLogInput, started time.Time) (bool, error) {
	resp, err := provider.ForwardStream(ctx, req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "text/event-stream" {
		return false, fmt.Errorf("upstream audio stream is not SSE")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	controller := http.NewResponseController(w)
	for scanner.Scan() {
		line := scanner.Bytes()
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if ok {
			var event struct {
				Type string `json:"type"`
			}
			data = bytes.TrimSpace(data)
			if jsonx.Unmarshal(data, &event) == nil {
				switch event.Type {
				case terminal:
					u := extractRawUsage(data, 0)
					log.Quota, log.PromptTokens, log.CompletionTokens = u.TotalTokens, u.PromptTokens, u.CompletionTokens
					log.ElapsedTime = time.Since(started).Milliseconds()
					if err := s.commitQuota(ctx, reservationID, u.TotalTokens, true, log); err != nil {
						return true, relaybiz.MarkPostForwardError(err)
					}
					s.ingestUsageLog(ctx, log)
					if _, err := fmt.Fprintf(w, "%s\n\n", line); err != nil {
						return true, relaybiz.MarkPostForwardError(err)
					}
					if terminal == "speech.audio.done" {
						_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
					}
					_ = controller.Flush()
					return true, nil
				case "error", "speech.audio.error":
					_ = s.releaseQuota(ctx, reservationID, "upstream audio stream failed")
					_, _ = fmt.Fprintf(w, "%s\n\n", line)
					_ = controller.Flush()
					return true, relaybiz.MarkPostForwardError(fmt.Errorf("upstream audio stream failed"))
				}
			}
		}
		if _, err := fmt.Fprintf(w, "%s\n", line); err != nil {
			_ = s.releaseQuota(ctx, reservationID, "audio response canceled")
			return true, relaybiz.MarkPostForwardError(err)
		}
		if len(line) == 0 {
			_ = controller.Flush()
		}
	}
	_ = s.releaseQuota(ctx, reservationID, "audio stream ended without completion")
	return true, relaybiz.MarkPostForwardError(fmt.Errorf("audio stream ended without completion"))
}
