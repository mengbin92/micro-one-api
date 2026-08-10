package provider

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStreamIdleTimeout is returned when an upstream stream stops producing
// bytes for the configured provider timeout.
var ErrStreamIdleTimeout = errors.New("upstream stream idle timeout")

var streamTransports sync.Map // map[time.Duration]*http.Transport

// newStreamHTTPClient builds a client without http.Client.Timeout (which would
// impose a hard deadline on an otherwise healthy long-lived SSE response).
// Instead, the transport bounds the response-header wait and wraps successful
// response bodies with a sliding idle timeout that resets whenever bytes arrive.
func newStreamHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &streamTimeoutRoundTripper{
			base:        streamTransport(timeout),
			idleTimeout: timeout,
		},
	}
}

func streamTransport(timeout time.Duration) *http.Transport {
	if cached, ok := streamTransports.Load(timeout); ok {
		return cached.(*http.Transport)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout
	actual, loaded := streamTransports.LoadOrStore(timeout, transport)
	if loaded {
		transport.CloseIdleConnections()
		return actual.(*http.Transport)
	}
	return transport
}

type streamTimeoutRoundTripper struct {
	base        http.RoundTripper
	idleTimeout time.Duration
}

func (t *streamTimeoutRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || t.idleTimeout <= 0 {
		return resp, err
	}
	resp.Body = newStreamIdleReadCloser(resp.Body, t.idleTimeout)
	return resp, nil
}

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
