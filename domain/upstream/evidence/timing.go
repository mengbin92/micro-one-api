package evidence

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"micro-one-api/domain/requesttrace"
)

func timedHTTPDo(client *http.Client, req *http.Request, protocol string) (*http.Response, error) {
	if req == nil {
		return client.Do(req)
	}
	ctx, end := requesttrace.Phase(req.Context(), "upstream.http")
	span := trace.SpanFromContext(ctx)
	if requesttrace.Observation(ctx) == nil && !span.IsRecording() {
		end(nil)
		return client.Do(req)
	}
	span.SetAttributes(attribute.String("upstream.protocol", protocol))
	var mu sync.Mutex
	pending := map[string][]trace.Span{}
	start := func(name string) {
		_, child := otel.Tracer("micro-one-api/network").Start(ctx, name)
		mu.Lock()
		pending[name] = append(pending[name], child)
		mu.Unlock()
	}
	finish := func(name string, err error) {
		mu.Lock()
		defer mu.Unlock()
		values := pending[name]
		if len(values) == 0 {
			return
		}
		child := values[len(values)-1]
		pending[name] = values[:len(values)-1]
		if err != nil {
			child.SetStatus(codes.Error, "failed")
		}
		child.End()
	}
	callbacks := &httptrace.ClientTrace{
		GetConn: func(string) { start("network.connection") },
		GotConn: func(info httptrace.GotConnInfo) {
			finish("network.connection", nil)
			span.SetAttributes(attribute.Bool("network.connection.reused", info.Reused), attribute.Bool("network.connection.idle", info.WasIdle))
		},
		DNSStart: func(httptrace.DNSStartInfo) { start("network.dns") }, DNSDone: func(info httptrace.DNSDoneInfo) { finish("network.dns", info.Err) },
		ConnectStart: func(string, string) { start("network.connect") }, ConnectDone: func(_, _ string, err error) { finish("network.connect", err) },
		TLSHandshakeStart: func() { start("network.tls") }, TLSHandshakeDone: func(_ tls.ConnectionState, err error) { finish("network.tls", err) },
		GotFirstResponseByte: func() { span.AddEvent("upstream.first_byte") },
	}
	resp, err := client.Do(req.WithContext(httptrace.WithClientTrace(ctx, callbacks))) // #nosec G704 -- same validated request/client as evidence.Do.
	mu.Lock()
	for _, spans := range pending {
		for _, child := range spans {
			child.End()
		}
	}
	mu.Unlock()
	if err != nil {
		end(err)
		return resp, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	body := &timedBody{ReadCloser: resp.Body, ctx: ctx, span: span, end: end, stream: strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"), status: resp.StatusCode}
	body.mu.Lock()
	body.stop = context.AfterFunc(ctx, func() { body.finish(ctx.Err()) })
	body.mu.Unlock()
	resp.Body = body
	return resp, nil
}

type timedBody struct {
	io.ReadCloser
	ctx                     context.Context
	span                    trace.Span
	end                     func(error)
	stop                    func() bool
	once                    sync.Once
	mu                      sync.Mutex
	probe                   requesttrace.ContentProbe
	firstBody, firstContent bool
	stream                  bool
	status                  int
}

func (b *timedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if n > 0 {
		if !b.firstBody {
			b.firstBody = true
			b.span.AddEvent("upstream.first_body")
		}
		if b.stream {
			b.probe.Feed(p[:n])
			if b.probe.Truncated {
				requesttrace.Missing(b.ctx, "upstream_frame_limit")
			}
			if !b.firstContent && b.probe.Kind != "" {
				b.firstContent = true
				b.span.AddEvent("upstream.first_content", trace.WithAttributes(attribute.String("output.kind", b.probe.Kind)))
			}
		}
	}
	terminal, failed := b.probe.Terminal, b.probe.Failed
	b.mu.Unlock()
	if terminal {
		b.span.AddEvent("upstream.terminal")
		if failed {
			b.finish(errors.New("upstream_failed"))
		} else {
			b.finish(nil)
		}
	} else if err != nil {
		if err == io.EOF && !b.stream && b.status >= 200 && b.status < 300 {
			b.finish(nil)
		} else {
			b.finish(errors.New("incomplete"))
		}
	}
	return n, err
}
func (b *timedBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish(errors.New("incomplete"))
	return err
}
func (b *timedBody) finish(err error) {
	b.once.Do(func() {
		b.mu.Lock()
		stop := b.stop
		b.mu.Unlock()
		if stop != nil {
			stop()
		}
		b.end(err)
	})
}
