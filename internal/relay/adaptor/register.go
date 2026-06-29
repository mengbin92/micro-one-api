package adaptor

import (
	"context"
	"io"
	"net/http"

	"micro-one-api/internal/relay/provider"
)

// This file's init() registers factory functions for every channel type that
// the existing provider.ProviderFactory already serves. Each factory lazily
// builds the underlying provider from the RelayContext at Init time, so the
// registry dispatch is cheap and stateless.
//
// The factories take a *provider.ProviderFactory so they can construct the
// concrete provider with the channel's base URL, key and config. Set it once
// at process start via SetProviderFactory.

var globalProviderFactory *provider.ProviderFactory

// SetProviderFactory wires the shared provider factory used by the adaptor
// registry. Must be called once during server bootstrap (see wire_gen.go).
func SetProviderFactory(f *provider.ProviderFactory) {
	globalProviderFactory = f
}

// providerFor builds a provider.Provider for the given channel type using the
// shared factory. Returns nil with an error if no factory is configured or the
// channel type is unsupported.
func providerFor(ctx *RelayContext) (provider.Provider, error) {
	if globalProviderFactory == nil {
		return nil, errNoFactory
	}
	if ctx == nil || ctx.Channel == nil {
		return nil, errNoChannel
	}
	cfg := provider.ProviderConfig{}
	if ctx.Channel.Config.APIVersion != "" {
		cfg.APIVersion = ctx.Channel.Config.APIVersion
	}
	return globalProviderFactory.CreateProviderWithConfig(
		ctx.Channel.Type, ctx.Channel.BaseURL, ctx.Channel.Key, cfg,
	)
}

// modelsFor returns the channel's explicit model list, or nil to let the
// adaptor use its own defaults.
func modelsFor(ctx *RelayContext) []string {
	if ctx == nil || ctx.Channel == nil {
		return nil
	}
	return ctx.Channel.Models
}

func init() {
	// OpenAI-compatible family (20+ types). They all resolve to an
	// OpenAIProvider and share the same adaptor shape.
	for _, t := range []int32{
		provider.ChannelTypeOpenAI,
		provider.ChannelTypeDeepSeek,
		provider.ChannelTypeMistral,
		provider.ChannelTypeMoonshot,
		provider.ChannelTypeGroq,
		provider.ChannelTypeCohere,
		provider.ChannelTypeBaichuan,
		provider.ChannelTypeZhipu,
		provider.ChannelTypeTongyi,
		provider.ChannelTypeMinimax,
		provider.ChannelTypeTogether,
		provider.ChannelTypeFireworks,
		provider.ChannelTypePerplexity,
		provider.ChannelTypeNovita,
		provider.ChannelTypeOpenRouter,
		provider.ChannelTypeSiliconFlow,
		provider.ChannelTypeOllama,
		provider.ChannelTypeDoubao,
		provider.ChannelTypeVoyageAI,
	} {
		t := t
		Register(t, func() Adaptor {
			return &lazyAdaptor{
				kind:   "openai_compatible",
				ctor:   func(ctx *RelayContext) (provider.Provider, error) { return providerFor(ctx) },
				models: func(ctx *RelayContext) []string { return modelsFor(ctx) },
				build: func(p provider.Provider, models []string) Adaptor {
					return NewOpenAICompatibleAdaptor(p, models)
				},
			}
		})
	}

	Register(provider.ChannelTypeAnthropic, func() Adaptor {
		return &lazyAdaptor{
			kind:   "anthropic",
			ctor:   func(ctx *RelayContext) (provider.Provider, error) { return providerFor(ctx) },
			models: func(ctx *RelayContext) []string { return modelsFor(ctx) },
			build: func(p provider.Provider, models []string) Adaptor {
				return NewAnthropicAdaptor(p, models)
			},
		}
	})

	Register(provider.ChannelTypeGemini, func() Adaptor {
		return &lazyAdaptor{
			kind:   "gemini",
			ctor:   func(ctx *RelayContext) (provider.Provider, error) { return providerFor(ctx) },
			models: func(ctx *RelayContext) []string { return modelsFor(ctx) },
			build: func(p provider.Provider, models []string) Adaptor {
				return NewGeminiAdaptor(p, models)
			},
		}
	})

	Register(provider.ChannelTypeAzure, func() Adaptor {
		return &lazyAdaptor{
			kind: "azure",
			ctor: func(ctx *RelayContext) (provider.Provider, error) { return providerFor(ctx) },
			models: func(ctx *RelayContext) []string {
				return modelsFor(ctx)
			},
			build: func(p provider.Provider, models []string) Adaptor {
				// APIVersion is read from the channel config at Init time.
				return NewAzureAdaptor(p, models, "")
			},
		}
	})
}

// lazyAdaptor defers provider construction until Init is called with a
// RelayContext. This lets the registry hand out cheap zero-state adaptor
// instances and only pay the provider-construction cost when a real relay
// context is available.
//
// Until Init is called, ModelList/Name return placeholders and the conversion
// methods return errNotInitialized. This is acceptable because the server
// layer always calls Init before any other method.
type lazyAdaptor struct {
	kind   string
	ctor   func(ctx *RelayContext) (provider.Provider, error)
	models func(ctx *RelayContext) []string
	build  func(p provider.Provider, models []string) Adaptor

	inner Adaptor
}

func (l *lazyAdaptor) Init(ctx *RelayContext) {
	p, err := l.ctor(ctx)
	if err != nil {
		l.inner = &errorAdaptor{kind: l.kind, err: err}
		return
	}
	l.inner = l.build(p, l.models(ctx))
	// Propagate Init to the concrete adaptor in case it needs context state
	// (none of the MVP adaptors do, but it keeps the contract honest).
	l.inner.Init(ctx)
}

func (l *lazyAdaptor) Name() string {
	if l.inner != nil {
		return l.inner.Name()
	}
	return l.kind
}

func (l *lazyAdaptor) ModelList() []string {
	if l.inner != nil {
		return l.inner.ModelList()
	}
	return nil
}

func (l *lazyAdaptor) ConvertRequest(ctx *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if l.inner == nil {
		return "", nil, errNotInitialized
	}
	return l.inner.ConvertRequest(ctx, inbound, body)
}

func (l *lazyAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	if l.inner == nil {
		return "", errNotInitialized
	}
	return l.inner.GetUpstreamURL(ctx)
}

func (l *lazyAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, upstream Format, body []byte) (*http.Request, error) {
	if l.inner == nil {
		return nil, errNotInitialized
	}
	return l.inner.BuildUpstreamRequest(ctx, rc, upstream, body)
}

func (l *lazyAdaptor) ConvertResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, []byte, error) {
	if l.inner == nil {
		return "", nil, errNotInitialized
	}
	return l.inner.ConvertResponse(ctx, upstream, resp)
}

func (l *lazyAdaptor) ConvertStreamResponse(ctx *RelayContext, upstream Format, resp *http.Response) (Format, io.Reader, error) {
	if l.inner == nil {
		return "", nil, errNotInitialized
	}
	return l.inner.ConvertStreamResponse(ctx, upstream, resp)
}

// errorAdaptor is a sink returned by lazyAdaptor.Init when the underlying
// provider could not be constructed. Every method surfaces the construction
// error so callers fail fast with a clear message.
type errorAdaptor struct {
	kind string
	err  error
}

func (e *errorAdaptor) Init(*RelayContext)  {}
func (e *errorAdaptor) Name() string        { return e.kind }
func (e *errorAdaptor) ModelList() []string { return nil }
func (e *errorAdaptor) ConvertRequest(*RelayContext, Format, []byte) (Format, []byte, error) {
	return "", nil, e.err
}
func (e *errorAdaptor) GetUpstreamURL(*RelayContext) (string, error) { return "", e.err }
func (e *errorAdaptor) BuildUpstreamRequest(context.Context, *RelayContext, Format, []byte) (*http.Request, error) {
	return nil, e.err
}
func (e *errorAdaptor) ConvertResponse(*RelayContext, Format, *http.Response) (Format, []byte, error) {
	return "", nil, e.err
}
func (e *errorAdaptor) ConvertStreamResponse(*RelayContext, Format, *http.Response) (Format, io.Reader, error) {
	return "", nil, e.err
}
