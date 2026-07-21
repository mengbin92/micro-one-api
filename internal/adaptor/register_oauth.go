package adaptor

import (
	"context"
	"io"
	"net/http"

	"micro-one-api/domain/upstream/credential"
	"micro-one-api/internal/identity"
	"micro-one-api/domain/upstream/provider"
)

// This file registers the subscription-account OAuth adaptors. Unlike the
// API-key adaptors (register.go) they are not backed by a provider.Provider;
// they own their upstream interaction via the credential + identity + apicompat
// layers. They are registered lazily and only materialized when a
// RelayContext carries an Account.

var (
	// globalTokenProviderFactory builds a TokenProvider for a platform. It is
	// wired at process start via SetTokenProviderFactory, analogous to
	// SetProviderFactory. When nil, the OAuth adaptors return a clear error.
	globalTokenProviderFactory TokenProviderFactory
	// globalIdentityService is the shared IdentityService used by the OAuth
	// adaptors to resolve fingerprints.
	globalIdentityService *identity.IdentityService
)

// TokenProviderFactory returns a TokenProvider for a platform. Set once at
// process start via SetTokenProviderFactory.
type TokenProviderFactory func(platform identity.Platform) credential.TokenProvider

// SetTokenProviderFactory wires the token-provider factory used by the OAuth
// adaptors. Must be called once during server bootstrap (see wire_gen.go).
func SetTokenProviderFactory(f TokenProviderFactory) {
	globalTokenProviderFactory = f
}

// SetIdentityService wires the shared IdentityService used by the OAuth
// adaptors to resolve account fingerprints.
func SetIdentityService(svc *identity.IdentityService) {
	globalIdentityService = svc
}

func init() {
	Register(provider.ChannelTypeClaudeOAuth, func() Adaptor {
		return &lazyOAuthAdaptor{platform: identity.PlatformClaude}
	})
	Register(provider.ChannelTypeCodexOAuth, func() Adaptor {
		return &lazyOAuthAdaptor{platform: identity.PlatformCodex}
	})
	// Domestic "Coding Plan" vendors - Anthropic-compatible upstreams.
	// Zhipu GLM and MiniMax use static API keys (StaticTokenProvider);
	// Kimi uses OAuth refresh (KimiTokenProvider, wired in P3). All three
	// reuse the Claude OAuth adaptor's request construction because their
	// upstreams speak the Anthropic Messages API; only the BaseURL (set on
	// the channel) and the token provider differ.
	// See docs/design/cn-subscription-accounts-roadmap.md.
	Register(provider.ChannelTypeZhipuPlan, func() Adaptor {
		return &lazyOAuthAdaptor{platform: identity.PlatformZhipu}
	})
	Register(provider.ChannelTypeMinimaxPlan, func() Adaptor {
		return &lazyOAuthAdaptor{platform: identity.PlatformMinimax}
	})
	Register(provider.ChannelTypeKimiOAuth, func() Adaptor {
		return &lazyOAuthAdaptor{platform: identity.PlatformKimi}
	})
}

// lazyOAuthAdaptor defers the construction of a concrete OAuth adaptor until
// Init is called with a RelayContext. This mirrors lazyAdaptor for the
// API-key family but resolves the token provider / identity service from the
// process-global wiring rather than from the provider factory.
type lazyOAuthAdaptor struct {
	platform identity.Platform
	inner    Adaptor
}

func (l *lazyOAuthAdaptor) Init(rc *RelayContext) {
	inner, err := l.build(rc)
	if err != nil {
		l.inner = &errorAdaptor{kind: l.kind(), err: err}
		return
	}
	l.inner = inner
}

func (l *lazyOAuthAdaptor) build(rc *RelayContext) (Adaptor, error) {
	var tp credential.TokenProvider
	if globalTokenProviderFactory != nil {
		tp = globalTokenProviderFactory(l.platform)
	}
	models := rc.Channel.Models
	switch l.platform {
	case identity.PlatformClaude:
		return NewClaudeOAuthAdaptor(tp, globalIdentityService, models), nil
	case identity.PlatformCodex:
		return NewCodexOAuthAdaptor(tp, globalIdentityService, models), nil
	case identity.PlatformZhipu, identity.PlatformMinimax, identity.PlatformKimi:
		// All three domestic vendors expose an Anthropic-compatible Messages
		// API, so the Claude OAuth adaptor's request construction, header
		// mimicry and response/stream conversion apply unchanged. The vendor
		// endpoint is selected via the channel's BaseURL (set at channel
		// creation); the token provider is resolved by the platform-tagged
		// factory wired in cmd/relay-gateway/wire.go.
		return NewClaudeOAuthAdaptor(tp, globalIdentityService, models), nil
	default:
		return nil, errUnknownOAuthPlatform
	}
}

func (l *lazyOAuthAdaptor) kind() string {
	return "oauth:" + string(l.platform)
}

func (l *lazyOAuthAdaptor) Name() string {
	if l.inner != nil {
		return l.inner.Name()
	}
	return l.kind()
}

func (l *lazyOAuthAdaptor) ModelList() []string {
	if l.inner != nil {
		return l.inner.ModelList()
	}
	return nil
}

func (l *lazyOAuthAdaptor) ConvertRequest(ctx *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if l.inner == nil {
		return "", nil, errNotInitialized
	}
	return l.inner.ConvertRequest(ctx, inbound, body)
}

func (l *lazyOAuthAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	if l.inner == nil {
		return "", errNotInitialized
	}
	return l.inner.GetUpstreamURL(ctx)
}

func (l *lazyOAuthAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, upstream Format, body []byte) (*http.Request, error) {
	if l.inner == nil {
		return nil, errNotInitialized
	}
	return l.inner.BuildUpstreamRequest(ctx, rc, upstream, body)
}

func (l *lazyOAuthAdaptor) ConvertResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	if l.inner == nil {
		return "", nil, errNotInitialized
	}
	return l.inner.ConvertResponse(ctx, upstream, resp)
}

func (l *lazyOAuthAdaptor) ConvertStreamResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	if l.inner == nil {
		return "", nil, errNotInitialized
	}
	return l.inner.ConvertStreamResponse(ctx, upstream, resp)
}
