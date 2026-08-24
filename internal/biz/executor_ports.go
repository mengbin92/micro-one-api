package biz

import (
	"context"
	"time"
)

// ExecutorRequest is the transport-neutral input for one relay execution.
// Transport adapters read and validate the request before constructing it;
// the executor never receives an http.Request or owns a response writer.
type ExecutorRequest struct {
	Token     string
	Model     string
	Endpoint  string
	Body      []byte
	Headers   map[string][]string
	RequestID string
	Stream    bool
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

// Forwarder sends one normalized request to the selected upstream source.
type Forwarder interface {
	Forward(context.Context, *RelayPlan, ExecutorRequest) (*ForwardResponse, error)
}

// EventLogger records the successful execution usage event. Error and route
// events remain owned by the existing selection/error instrumentation until
// their transport-neutral event shape is introduced.
type EventLogger interface {
	LogUsage(context.Context, *RelayPlan, ExecutorRequest, CanonicalUsage, time.Duration, bool)
}
