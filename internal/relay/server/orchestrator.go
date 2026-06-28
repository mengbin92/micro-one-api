package server

import (
	"context"
	"io"
	"net/http"
	"time"

	relaybiz "micro-one-api/internal/relay/biz"
)

// APIEndpoint represents a specific API endpoint type.
type APIEndpoint string

const (
	EndpointChatCompletions   APIEndpoint = "chat/completions"
	EndpointCompletions       APIEndpoint = "completions"
	EndpointEmbeddings        APIEndpoint = "embeddings"
	EndpointImagesGenerations APIEndpoint = "images/generations"
	EndpointAudioTranscribe   APIEndpoint = "audio/transcriptions"
	EndpointAudioTranslate    APIEndpoint = "audio/translations"
	EndpointAudioSpeech       APIEndpoint = "audio/speech"
	EndpointModerations       APIEndpoint = "moderations"
	EndpointResponses         APIEndpoint = "responses"
	EndpointAnthropicMessages APIEndpoint = "anthropic/messages"
	EndpointModels            APIEndpoint = "models"
	EndpointUsage             APIEndpoint = "usage"
)

// Orchestrator coordinates the complete relay request lifecycle:
// auth → model mapping → channel selection → reserve → forward → commit → log
type Orchestrator interface {
	// Execute runs the complete relay pipeline for a request.
	Execute(ctx context.Context, req *RelayRequest) (*RelayResult, error)
}

// RelayRequest is the normalized input for orchestration.
type RelayRequest struct {
	// Token is the Bearer token from Authorization header.
	Token string
	// Model is the model name requested by the client.
	Model string
	// Endpoint specifies which API endpoint is being called.
	Endpoint APIEndpoint
	// Body contains the raw request body.
	Body io.Reader
	// IsStream indicates if the client expects a streaming response.
	IsStream bool
	// Headers contains the original HTTP headers.
	Headers http.Header
	// ClientID is a unique identifier for the client (for sticky routing).
	ClientID string
	// RequestID is a unique identifier for this request (for idempotency).
	RequestID string
}

// RelayResult contains the response and metadata from orchestration.
type RelayResult struct {
	// Response is the upstream response body (may be streaming).
	Response io.ReadCloser
	// Headers contains the HTTP headers from the upstream response.
	Headers http.Header
	// StatusCode is the HTTP status code.
	StatusCode int
	// Usage contains token usage information for billing.
	Usage *Usage
	// ChannelID is the selected channel ID.
	ChannelID int64
	// SubscriptionAccountID is the selected subscription account ID (if applicable).
	SubscriptionAccountID int64
	// Latency is the total orchestration duration.
	Latency time.Duration
	// Error contains any error that occurred (non-nil if StatusCode >= 400).
	Error error
}

// Usage represents token usage information from the upstream response.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// OrchestratorConfig holds configuration for the orchestrator.
type OrchestratorConfig struct {
	// MaxAttempts is the maximum number of retry attempts (including initial).
	MaxAttempts int
	// ReserveTimeout is the timeout for quota reservation.
	ReserveTimeout time.Duration
	// CommitTimeout is the timeout for quota commit.
	CommitTimeout time.Duration
	// ForwardTimeout is the timeout for upstream forwarding.
	ForwardTimeout time.Duration
	// EnableRetry enables retry logic.
	EnableRetry bool
	// EnableFailover enables channel failover on retry.
	EnableFailover bool
}

// DefaultOrchestratorConfig returns the default orchestrator configuration.
func DefaultOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		MaxAttempts:    3,
		ReserveTimeout: 5 * time.Second,
		CommitTimeout:  5 * time.Second,
		ForwardTimeout: 300 * time.Second,
		EnableRetry:    true,
		EnableFailover: true,
	}
}

// relayOrchestrator is the concrete implementation of Orchestrator.
type relayOrchestrator struct {
	config     *OrchestratorConfig
	relayUsecase *relaybiz.RelayUsecase
	// Additional dependencies will be added:
	// - billing coordinator
	// - forwarder factory
	// - logger
}

// NewRelayOrchestrator creates a new orchestrator instance.
func NewRelayOrchestrator(relayUsecase *relaybiz.RelayUsecase, cfg *OrchestratorConfig) Orchestrator {
	if cfg == nil {
		cfg = DefaultOrchestratorConfig()
	}
	return &relayOrchestrator{
		config:      cfg,
		relayUsecase: relayUsecase,
	}
}

// Execute runs the complete relay pipeline.
//
// The pipeline consists of the following stages:
//
// 1. Auth Validation: Verify token and get user context
// 2. Model Mapping: Resolve client model to upstream model
// 3. Channel Selection: Select appropriate channel for the request
// 4. Quota Reservation: Reserve quota for the estimated cost
// 5. Request Forwarding: Forward request to upstream provider
// 6. Response Processing: Process response and extract usage
// 7. Quota Commit/Release: Commit actual usage or release reservation on error
// 8. Logging: Log the request for billing and analytics
func (o *relayOrchestrator) Execute(ctx context.Context, req *RelayRequest) (*RelayResult, error) {
	startTime := time.Now()
	result := &RelayResult{}

	// Stage 1-3: Planning (auth, model mapping, channel selection)
	// This reuses the existing RelayUsecase.Plan() logic
	plan, err := o.relayUsecase.Plan(ctx, relaybiz.RelayRequest{
		Token: req.Token,
		Model: req.Model,
	})
	if err != nil {
		result.Error = err
		result.StatusCode = statusCodeFromError(err)
		result.Latency = time.Since(startTime)
		return result, err
	}

	// Store resolved information in result
	result.ChannelID = plan.Channel.ID
	if plan.Account != nil {
		result.SubscriptionAccountID = plan.Account.ID
	}

	// TODO: Stage 4: Quota Reservation
	// This will be implemented by the BillingCoordinator

	// TODO: Stage 5: Request Forwarding
	// This will be implemented by the Forwarder

	// TODO: Stage 6-7: Response Processing and Quota Commit/Release

	// TODO: Stage 8: Logging

	result.Latency = time.Since(startTime)
	return result, nil
}

// statusCodeFromError converts an error to an HTTP status code.
func statusCodeFromError(err error) int {
	// This is a placeholder - actual implementation will check error types
	// and return appropriate status codes (401, 403, 429, 500, etc.)
	return http.StatusInternalServerError
}
