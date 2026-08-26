package biz

import (
	"context"
	"time"
)

// ExecutorRequest is the transport-neutral input for one relay execution.
// Transport adapters read and validate the request before constructing it;
// the executor never receives an http.Request or owns a response writer.
type ExecutorRequest struct {
	Token       string
	Model       string
	Endpoint    string
	Body        []byte
	Headers     map[string][]string
	RequestID   string
	SessionHash string
	Stream      bool
}

// ExecutionResponse is the transport-neutral result of an upstream call.
// Body is owned by the caller and contains the complete non-stream response.
type ExecutionResponse struct {
	StatusCode            int
	Headers               map[string][]string
	Body                  []byte
	Usage                 *CanonicalUsage
	ChannelID             int64
	SubscriptionAccountID int64
	RequestID             string
	// Stream is set only for streaming executions. The transport adapter owns
	// draining and closing it; Body remains empty for these responses.
	Stream RelayStream
}

// Executor is the business execution boundary shared by transport adapters.
type Executor interface {
	Execute(context.Context, ExecutorRequest) (ExecutionResponse, error)
}

// Planner resolves authentication, model mapping, routing, and retry
// candidates. RelayUsecase satisfies this interface directly.
type Planner interface {
	Plan(context.Context, RelayRequest) (*RelayPlan, error)
}

// QuotaReservation is the transport-neutral handle passed between reserve,
// commit, and release.
type QuotaReservation struct {
	ID string
}

// QuotaPort owns the quota lifecycle for one execution attempt.
type QuotaPort interface {
	Reserve(context.Context, *RelayPlan, ExecutorRequest, CanonicalUsage) (*QuotaReservation, error)
	Commit(context.Context, *RelayPlan, ExecutorRequest, *QuotaReservation, CanonicalUsage, bool, time.Duration) error
	Release(context.Context, *QuotaReservation, string) error
}

// ForwardResponse contains the provider response without transport-owned
// readers or writers.
type ForwardResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	Usage      *CanonicalUsage
}

// RelayStream is the transport-neutral byte stream returned by a streaming
// forwarder. It deliberately mirrors io.ReadCloser without importing HTTP,
// SSE, or WebSocket types into the business boundary.
type RelayStream interface {
	Read([]byte) (int, error)
	Close() error
}

// StreamForwardResponse contains headers and a live upstream byte stream.
// Usage is finalized by the executor as the stream is drained.
type StreamForwardResponse struct {
	StatusCode int
	Headers    map[string][]string
	Stream     RelayStream
}

// Forwarder sends one normalized request to the selected upstream source.
type Forwarder interface {
	Forward(context.Context, *RelayPlan, ExecutorRequest) (*ForwardResponse, error)
}

// StreamForwarder sends one streaming request through the adaptor registry.
type StreamForwarder interface {
	ForwardStream(context.Context, *RelayPlan, ExecutorRequest) (*StreamForwardResponse, error)
}

// EventLogger records the successful execution usage event. Error and route
// events remain owned by the existing selection/error instrumentation until
// their transport-neutral event shape is introduced.
type EventLogger interface {
	LogUsage(context.Context, *RelayPlan, ExecutorRequest, CanonicalUsage, time.Duration, bool)
}
