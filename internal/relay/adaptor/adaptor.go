// Package adaptor provides the unified upstream adapter abstraction for the
// relay gateway.
//
// It mirrors the design of new-api's relay/channel/adapter.go: each upstream
// (an API-key channel or a subscription account) is represented by an Adaptor
// that is responsible for converting the inbound client protocol into the
// upstream protocol, building the final http.Request (including any identity
// mimicry), and converting the upstream response back to the client's expected
// outbound protocol.
//
// MVP scope (plan §十): this layer is a pure wrapper over the existing
// provider.Provider implementations and does not yet replace the server call
// sites. Existing /v1/chat/completions flows continue to call ProviderFactory
// directly; the Adaptor registry is exercised by tests and is available for
// the feature-flag-controlled new path introduced in later phases.
package adaptor

import (
	"context"
	"io"
	"net/http"

	relaybiz "micro-one-api/internal/relay/biz"
)

// Format identifies a wire protocol, used as a dimension of the conversion
// matrix. See apicompat for the actual converters.
type Format string

const (
	FormatOpenAIChatCompletions Format = "chat_completions"
	FormatOpenAIResponses       Format = "responses"
	FormatAnthropicMessages     Format = "anthropic_messages"
	FormatGemini                Format = "gemini"
)

// ChannelRef is a read-only view of the selected API-key channel. It mirrors
// the fields of relaybiz.Channel that an adaptor needs to build an upstream
// request.
type ChannelRef = relaybiz.Channel

// AccountRef is a read-only view of the selected subscription account. It is
// populated only by the subscription-account selector (Phase 3+); for API-key
// channels Account is nil.
type AccountRef struct {
	ID          int64
	Platform    string // "codex" | "claude"
	AccountType string // "oauth" | "setup_token"
	GroupID     string
	AccessToken string
	AccountID   string // upstream account id (e.g. chatgpt-account-id)
	Fingerprint string // cached fingerprint snapshot (opaque)
	Status      string
	ExpiresAt   int64
}

// RelayContext carries the full context of a single relay. It replaces the
// loose collection of parameters that previously threaded through the server
// handlers.
type RelayContext struct {
	InboundFormat Format      // client inbound protocol
	ClientModel   string      // model name requested by the client
	ResolvedModel string      // upstream model name after mapping
	Channel       *ChannelRef // selected API-key channel (nil for subscriptions)
	Account       *AccountRef // selected subscription account (nil for channels)
	IsStream      bool
	UserID        int64
	RequestID     string
	RawBody       []byte // client raw request body (for passthrough/mimicry)

	// InboundHeader is the client's original request headers. OAuth adaptors use
	// it to detect genuine first-party clients (so mimicry is only applied to
	// third-party callers) and to forward client-specific headers. API-key
	// adaptors ignore it.
	InboundHeader http.Header

	// HTTPClient is the client used to call the upstream. Adaptors MAY use it
	// in BuildUpstreamRequest when they need to issue the request themselves;
	// the MVP providers own their own client and ignore this field.
	HTTPClient *http.Client
}

// Adaptor is the unified upstream adapter interface.
//
// Each implementation decides its own upstream protocol: an OpenAI-compatible
// adaptor speaks chat_completions, a Codex subscription adaptor speaks
// responses, a Claude OAuth adaptor speaks anthropic_messages. ConvertRequest
// bridges the inbound format to that upstream format.
type Adaptor interface {
	// Init seeds the adaptor with the relay context. Analogous to new-api's
	// adaptor.Init.
	Init(ctx *RelayContext)

	// ConvertRequest converts the client request body to the upstream format.
	// It returns the upstream format and the converted body. When the inbound
	// format already matches the upstream format it may return the body
	// unchanged.
	ConvertRequest(ctx *RelayContext, inbound Format, body []byte) (Format, []byte, error)

	// GetUpstreamURL returns the upstream target URL.
	GetUpstreamURL(ctx *RelayContext) (string, error)

	// BuildUpstreamRequest constructs the http.Request sent to the upstream,
	// including headers, authorization and any identity mimicry.
	BuildUpstreamRequest(ctx context.Context, rc *RelayContext, upstream Format, body []byte) (*http.Request, error)

	// ConvertResponse converts a non-streaming upstream response body to the
	// client's outbound format. The returned outbound Format should match the
	// inbound format of the request.
	ConvertResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error)

	// ConvertStreamResponse converts a streaming upstream response to the
	// client's outbound format, returning a reader the caller drains.
	ConvertStreamResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error)

	// ModelList returns the models this adaptor supports.
	ModelList() []string

	// Name returns a human-readable adaptor name.
	Name() string
}
