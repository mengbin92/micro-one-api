package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
)

// openAI WS upstream client constants. These mirror the Codex Responses
// WebSocket protocol limits used by sub2api: a single event frame (such as a
// large rate_limits or delta) can exceed the coder/websocket default read
// limit of 32 KiB, so we raise it explicitly.
const (
	openAIWSUpstreamReadLimitBytes int64 = 16 * 1024 * 1024
	openAIWSDialTimeoutDefault           = 30 * time.Second
)

// openAIWSFrameConn abstracts the read/write/close surface of a WebSocket
// connection so the relay loop can operate on either the client-side or
// upstream-side connection through a single interface. This mirrors sub2api's
// openai_ws_v2.FrameConn.
type openAIWSFrameConn interface {
	ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error)
	WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error
	Close() error
}

// coderWSFrameConn adapts a *coderws.Conn to the openAIWSFrameConn interface.
type coderWSFrameConn struct {
	conn *coderws.Conn
}

func (c *coderWSFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.conn == nil {
		return coderws.MessageText, nil, errOpenAIWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	msgType, payload, err := c.conn.Read(ctx)
	if err != nil {
		return coderws.MessageText, nil, err
	}
	return msgType, payload, nil
}

func (c *coderWSFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.conn == nil {
		return errOpenAIWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.conn.Write(ctx, msgType, payload)
}

func (c *coderWSFrameConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_ = c.conn.Close(coderws.StatusNormalClosure, "")
	_ = c.conn.CloseNow()
	return nil
}

// errOpenAIWSConnClosed is returned by frame-conn wrappers when the underlying
// coder/websocket connection is nil or already closed.
var errOpenAIWSConnClosed = errors.New("openai ws connection closed")

// openAIWSUpstreamDialer abstracts dialing an upstream Codex Responses
// WebSocket endpoint. The concrete implementation uses coder/websocket.Dial.
type openAIWSUpstreamDialer interface {
	Dial(ctx context.Context, wsURL string, headers http.Header) (openAIWSFrameConn, int, http.Header, error)
}

type coderWSUpstreamDialer struct{}

func newCoderWSUpstreamDialer() openAIWSUpstreamDialer {
	return &coderWSUpstreamDialer{}
}

// Dial connects to an upstream Responses WebSocket endpoint. It returns the
// upgraded connection, the HTTP status code from the handshake (0 on success),
// and any response headers. Following sub2api, it enables permessage-deflate
// with context takeover and raises the read limit to accommodate large Codex
// event frames.
func (d *coderWSUpstreamDialer) Dial(ctx context.Context, wsURL string, headers http.Header) (openAIWSFrameConn, int, http.Header, error) {
	targetURL := strings.TrimSpace(wsURL)
	if targetURL == "" {
		return nil, 0, nil, errors.New("openai ws url is empty")
	}
	opts := &coderws.DialOptions{
		HTTPHeader:      cloneHeader(headers),
		CompressionMode: coderws.CompressionContextTakeover,
	}
	conn, resp, err := coderws.Dial(ctx, targetURL, opts)
	if err != nil {
		status := 0
		var respHeaders http.Header
		if resp != nil {
			status = resp.StatusCode
			respHeaders = cloneHeader(resp.Header)
		}
		return nil, status, respHeaders, err
	}
	conn.SetReadLimit(openAIWSUpstreamReadLimitBytes)
	var respHeaders http.Header
	if resp != nil {
		respHeaders = cloneHeader(resp.Header)
	}
	return &coderWSFrameConn{conn: conn}, 0, respHeaders, nil
}

// cloneHeader returns a shallow copy of an HTTP header so callers can mutate
// it without affecting the original. This mirrors sub2api's cloneHeader.
func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	copied := make(http.Header, len(h))
	for k, vs := range h {
		if len(vs) == 0 {
			continue
		}
		dup := make([]string, len(vs))
		copy(dup, vs)
		copied[k] = dup
	}
	return copied
}
