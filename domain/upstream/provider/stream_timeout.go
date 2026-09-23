package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"micro-one-api/platform/metrics"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStreamIdleTimeout is returned when an upstream stream stops producing
// bytes for the configured provider timeout.
var ErrStreamIdleTimeout = errors.New("upstream stream idle timeout")

var streamTransports sync.Map // map[streamTransportKey]*http.Transport

type streamTransportKey struct {
	timeout    time.Duration
	allowLocal bool
}

// newStreamHTTPClient builds a client without http.Client.Timeout (which would
// impose a hard deadline on an otherwise healthy long-lived SSE response).
// Instead, the transport bounds the response-header wait and wraps successful
// response bodies with a sliding idle timeout that resets whenever bytes arrive.
func newStreamHTTPClient(timeout time.Duration) *http.Client {
	return newStreamHTTPClientWithLocalAccess(timeout, false)
}

func newStreamHTTPClientWithLocalAccess(timeout time.Duration, allowLocal bool) *http.Client {
	cfg, err := StreamTimeoutsFromEnv(timeout)
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Transport: &streamTimeoutRoundTripper{
			base:          streamTransport(cfg.Header, allowLocal),
			idleTimeout:   cfg.Idle,
			headerTimeout: cfg.Header,
		},
		CheckRedirect: upstreamRedirectPolicy(allowLocal),
	}
}

// NewStreamHTTPClient exposes the provider-standard SSE client to unified
// adaptor executors. It bounds response-header and idle-byte waits without
// imposing a hard deadline on a healthy long-lived stream.
func NewStreamHTTPClient(timeout time.Duration) *http.Client {
	return newStreamHTTPClient(timeout)
}

func streamTransport(timeout time.Duration, allowLocal bool) *http.Transport {
	key := streamTransportKey{timeout: timeout, allowLocal: allowLocal}
	if cached, ok := streamTransports.Load(key); ok {
		return cached.(*http.Transport)
	}
	transport := newUpstreamTransport(timeout, allowLocal)
	actual, loaded := streamTransports.LoadOrStore(key, transport)
	if loaded {
		transport.CloseIdleConnections()
		return actual.(*http.Transport)
	}
	return transport
}

type streamTimeoutRoundTripper struct {
	headerTimeout time.Duration
	base          http.RoundTripper
	idleTimeout   time.Duration
}

func (t *streamTimeoutRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	var timer *time.Timer
	if t.headerTimeout > 0 {
		timer = time.AfterFunc(t.headerTimeout, func() { cancel(ErrStreamHeaderTimeout) })
	}
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if timer != nil {
		timer.Stop()
	}
	if err != nil || resp == nil || resp.Body == nil {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		cancel(nil)
		if err != nil {
			metrics.StreamTerminations.WithLabelValues(StreamFailureReason(err)).Inc()
		}
		return resp, err
	}
	resp.Body = &streamContextBody{ReadCloser: newStreamIdleReadCloser(resp.Body, t.idleTimeout), ctx: ctx, cancel: cancel}
	return resp, nil
}

type streamContextBody struct {
	io.ReadCloser
	ctx    context.Context
	cancel context.CancelCauseFunc
	once   sync.Once
}

func (b *streamContextBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		if cause := context.Cause(b.ctx); cause != nil {
			err = cause
		}
		b.once.Do(func() {
			if err != io.EOF {
				metrics.StreamTerminations.WithLabelValues(StreamFailureReason(err)).Inc()
			}
		})
	}
	return n, err
}
func (b *streamContextBody) Close() error { b.cancel(nil); return b.ReadCloser.Close() }

type streamIdleReadCloser struct {
	body        io.ReadCloser
	idleTimeout time.Duration
	activity    chan struct{}
	done        chan struct{}
	timedOut    atomic.Bool
	doneOnce    sync.Once
	closeOnce   sync.Once
	closeErr    error
}

func newStreamIdleReadCloser(body io.ReadCloser, idleTimeout time.Duration) io.ReadCloser {
	if body == nil || idleTimeout <= 0 {
		return body
	}
	r := &streamIdleReadCloser{
		body:        body,
		idleTimeout: idleTimeout,
		activity:    make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	go r.watch()
	return r
}

func (r *streamIdleReadCloser) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.touch()
	}
	if err != nil {
		r.doneOnce.Do(func() { close(r.done) })
	}
	if err != nil && r.timedOut.Load() {
		return n, fmt.Errorf("%w after %s", ErrStreamIdleTimeout, r.idleTimeout)
	}
	return n, err
}

func (r *streamIdleReadCloser) Close() error {
	r.doneOnce.Do(func() { close(r.done) })
	return r.closeBody()
}

func (r *streamIdleReadCloser) watch() {
	timer := time.NewTimer(r.idleTimeout)
	defer timer.Stop()
	for {
		select {
		case <-r.activity:
			resetStreamIdleTimer(timer, r.idleTimeout)
		case <-timer.C:
			// Prefer already-buffered activity over a simultaneous timer firing.
			select {
			case <-r.activity:
				resetStreamIdleTimer(timer, r.idleTimeout)
				continue
			default:
			}
			r.timedOut.Store(true)
			_ = r.closeBody()
			return
		case <-r.done:
			return
		}
	}
}

func (r *streamIdleReadCloser) touch() {
	select {
	case r.activity <- struct{}{}:
	default:
	}
}

func (r *streamIdleReadCloser) closeBody() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.body.Close()
	})
	return r.closeErr
}

func resetStreamIdleTimer(timer *time.Timer, timeout time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}
