package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/server/forwarder"
)

var _ relaybiz.Executor = relayExecutorAdapter{}

// relayExecutorAdapter exposes the server's staged orchestrator through the
// business executor contract. The adapter only supports the v0.23 non-stream
// slice; SSE and WebSocket remain owned by their existing transport handlers.
type relayExecutorAdapter struct {
	orchestrator *relayOrchestrator
}

func (a relayExecutorAdapter) Execute(ctx context.Context, req relaybiz.ExecutorRequest) (relaybiz.ExecutionResponse, error) {
	if req.Stream {
		return relaybiz.ExecutionResponse{}, fmt.Errorf("transport-neutral executor supports non-stream requests only")
	}
	if a.orchestrator == nil {
		return relaybiz.ExecutionResponse{}, fmt.Errorf("relay executor unavailable")
	}
	relayReq := relayRequestFromExecutorRequest(req)
	result, executeErr := a.orchestrator.Execute(ctx, relayReq)
	if result == nil {
		return relaybiz.ExecutionResponse{RequestID: relayReq.RequestID}, executeErr
	}
	response := relaybiz.ExecutionResponse{
		StatusCode:            result.StatusCode,
		Headers:               httpHeaderToMap(result.Headers),
		Usage:                 result.Usage,
		ChannelID:             result.ChannelID,
		SubscriptionAccountID: result.SubscriptionAccountID,
		RequestID:             relayReq.RequestID,
	}
	if result.Response != nil {
		body, readErr := io.ReadAll(result.Response)
		closeErr := result.Response.Close()
		response.Body = body
		if executeErr == nil {
			executeErr = readErr
			if executeErr == nil {
				executeErr = closeErr
			}
		}
	}
	return response, executeErr
}

// relayQuotaPort adapts the server-owned billing hooks to the business quota
// port without exposing HTTP request types to the executor.
type relayQuotaPort struct {
	hooks RelayLifecycleHooks
}

func (p relayQuotaPort) Reserve(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest, estimated relaybiz.CanonicalUsage) (*relaybiz.QuotaReservation, error) {
	if p.hooks == nil {
		return nil, nil
	}
	reservation, err := p.hooks.ReserveQuota(ctx, plan, relayRequestFromExecutorRequest(req), estimated)
	if err != nil || reservation == nil {
		return nil, err
	}
	return &relaybiz.QuotaReservation{ID: reservation.ID}, nil
}

func (p relayQuotaPort) Commit(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest, reservation *relaybiz.QuotaReservation, usage relaybiz.CanonicalUsage, success bool, latency time.Duration) error {
	if p.hooks == nil || reservation == nil {
		return nil
	}
	return p.hooks.CommitQuota(ctx, plan, relayRequestFromExecutorRequest(req), &Reservation{ID: reservation.ID}, usage, success, latency)
}

func (p relayQuotaPort) Release(ctx context.Context, reservation *relaybiz.QuotaReservation, reason string) error {
	if p.hooks == nil || reservation == nil {
		return nil
	}
	return p.hooks.ReleaseQuota(ctx, &Reservation{ID: reservation.ID}, reason)
}

// relayEventLogger adapts the existing usage logger to the business port.
type relayEventLogger struct {
	hooks RelayLifecycleHooks
}

func (l relayEventLogger) LogUsage(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest, usage relaybiz.CanonicalUsage, latency time.Duration, stream bool) {
	if l.hooks == nil {
		return
	}
	l.hooks.LogUsage(ctx, plan, relayRequestFromExecutorRequest(req), usage, latency, stream)
}

// relayNonStreamForwarder adapts the existing ProviderFactory-backed
// forwarder. The adapter consumes the provider reader and returns owned bytes,
// so the business executor has no io.Reader dependency.
type relayNonStreamForwarder struct {
	forwarder *forwarder.NonStreamForwarder
}

func (f relayNonStreamForwarder) Forward(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (*relaybiz.ForwardResponse, error) {
	if f.forwarder == nil {
		return nil, fmt.Errorf("non-stream forwarder unavailable")
	}
	resp, bodyReader, usage, err := f.forwarder.ForwardRequest(ctx, plan, endpointPath(APIEndpoint(req.Endpoint)), req.Body, headerMapToHTTP(req.Headers))
	if err != nil {
		return nil, err
	}
	if resp == nil || bodyReader == nil {
		return nil, fmt.Errorf("non-stream forwarder returned an incomplete response")
	}
	body, readErr := io.ReadAll(bodyReader)
	closeErr := bodyReader.Close()
	responseCloseErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if responseCloseErr != nil {
		return nil, responseCloseErr
	}
	return &relaybiz.ForwardResponse{
		StatusCode: resp.StatusCode,
		Headers:    httpHeaderToMap(resp.Header),
		Body:       body,
		Usage:      usage,
	}, nil
}

func relayRequestFromExecutorRequest(req relaybiz.ExecutorRequest) *RelayRequest {
	return &RelayRequest{
		Token:     req.Token,
		Model:     req.Model,
		Endpoint:  APIEndpoint(req.Endpoint),
		Body:      bytes.NewReader(req.Body),
		IsStream:  req.Stream,
		Headers:   headerMapToHTTP(req.Headers),
		RequestID: req.RequestID,
	}
}

func headerMapToHTTP(headers map[string][]string) http.Header {
	result := make(http.Header, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func httpHeaderToMap(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}
