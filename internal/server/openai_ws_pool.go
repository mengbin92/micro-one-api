package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"micro-one-api/domain/requesttrace"
	relayprovider "micro-one-api/domain/upstream/provider"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// openAIWSConnPool keeps only unused upstream connections. Once a client
// frame has been sent the session owns the upstream until both pumps exit.
type openAIWSConnPool struct {
	dialer             openAIWSUpstreamDialer
	dialTimeout        time.Duration
	maxIdle            time.Duration
	maxConnsPerChannel int

	mu   sync.Mutex
	idle map[string][]*openAIWSPooledConn

	acquireTotal  atomic.Int64
	acquireReuse  atomic.Int64
	acquireCreate atomic.Int64
	acquireFail   atomic.Int64
}

type openAIWSPooledConn struct {
	conn        openAIWSFrameConn
	poolKey     string
	fingerprint [sha256.Size]byte
	idleTimer   *time.Timer
}

const (
	openAIWSPoolDefaultMaxConnsPerChannel = 8
	openAIWSPoolDefaultMaxIdle            = 5 * time.Minute
)

func newOpenAIWSConnPool(dialTimeout time.Duration, client *http.Client) *openAIWSConnPool {
	return &openAIWSConnPool{
		dialer:             newCoderWSUpstreamDialer(client),
		dialTimeout:        dialTimeout,
		maxIdle:            openAIWSPoolDefaultMaxIdle,
		maxConnsPerChannel: openAIWSPoolDefaultMaxConnsPerChannel,
		idle:               make(map[string][]*openAIWSPooledConn),
	}
}

// Include every handshake header without retaining the credential in the pool.
func openAIWSConnFingerprint(wsURL string, headers http.Header) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = io.WriteString(hash, wsURL+"\x00")
	_ = headers.Write(hash)
	return [sha256.Size]byte(hash.Sum(nil))
}

// AcquireOrDial returns a usable upstream connection for the given source.
// It first checks the pool for a reusable idle connection; if none is
// available it dials a new one. The caller MUST call Release when done (which
// returns the connection to the pool, or closes it if the pool is full/closed).
func (p *openAIWSConnPool) AcquireOrDial(ctx context.Context, poolKey string, wsURL string, headers http.Header) (*openAIWSPooledConn, error) {
	p.acquireTotal.Add(1)
	fingerprint := openAIWSConnFingerprint(wsURL, headers)
	p.mu.Lock()
	if p.idle == nil {
		p.mu.Unlock()
		return nil, errOpenAIWSPoolClosed
	}
	var reused *openAIWSPooledConn
	var stale []*openAIWSPooledConn
	for len(p.idle[poolKey]) > 0 {
		conns := p.idle[poolKey]
		pc := conns[len(conns)-1]
		pc.idleTimer.Stop()
		p.removeIdle(pc)
		if pc.fingerprint == fingerprint {
			reused = pc
			break
		}
		stale = append(stale, pc)
	}
	p.mu.Unlock()
	for _, pc := range stale {
		_ = pc.conn.Close()
	}
	if reused != nil {
		p.acquireReuse.Add(1)
		// Reuse is charged to the acquiring turn: mark it on the enclosing
		// ws.pool.acquire phase span and skip any dial phase.
		trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("ws.pool.reused", true))
		return reused, nil
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("ws.pool.reused", false))
	conn, err := dialOpenAIWSUpstream(ctx, p.dialer, p.dialTimeout, wsURL, headers)
	if err != nil {
		p.acquireFail.Add(1)
		return nil, err
	}
	p.acquireCreate.Add(1)
	return &openAIWSPooledConn{conn: conn, poolKey: poolKey, fingerprint: fingerprint}, nil
}

func dialOpenAIWSUpstream(ctx context.Context, dialer openAIWSUpstreamDialer, timeout time.Duration, wsURL string, headers http.Header) (openAIWSFrameConn, error) {
	dialPhaseCtx, endDial := requesttrace.Phase(ctx, "ws.upstream.dial")
	dialCtx, cancel := context.WithTimeout(dialPhaseCtx, timeout)
	defer cancel()
	conn, statusCode, _, err := dialer.Dial(dialCtx, wsURL, headers)
	endDial(err)
	if err != nil {
		if statusCode > 0 {
			return nil, errors.Join(&relayprovider.UpstreamHTTPError{StatusCode: statusCode}, err)
		}
		return nil, err
	}
	return conn, nil
}

// Release returns a connection to the pool for reuse, or closes it if the
// pool is full, closed, or the connection is marked broken (broken=true).
func (p *openAIWSConnPool) Release(pc *openAIWSPooledConn, broken bool) {
	if pc == nil {
		return
	}
	p.mu.Lock()
	if broken || p.idle == nil || len(p.idle[pc.poolKey]) >= p.maxConnsPerChannel {
		p.mu.Unlock()
		_ = pc.conn.Close()
		return
	}
	p.idle[pc.poolKey] = append(p.idle[pc.poolKey], pc)
	// Stop may race an expiry callback that already started. Only the timer
	// for this particular release may retire the connection.
	var timer *time.Timer
	timer = time.AfterFunc(p.maxIdle, func() {
		p.mu.Lock()
		expired := pc.idleTimer == timer && p.removeIdle(pc)
		p.mu.Unlock()
		if expired {
			_ = pc.conn.Close()
		}
	})
	pc.idleTimer = timer
	p.mu.Unlock()
}

// removeIdle is called under mu. Leased connections never appear in idle.
func (p *openAIWSConnPool) removeIdle(pc *openAIWSPooledConn) bool {
	conns := p.idle[pc.poolKey]
	i := slices.Index(conns, pc)
	if i < 0 {
		return false
	}
	conns = slices.Delete(conns, i, i+1)
	if len(conns) == 0 {
		delete(p.idle, pc.poolKey)
	} else {
		p.idle[pc.poolKey] = conns
	}
	return true
}

func (pc *openAIWSPooledConn) FrameConn() openAIWSFrameConn { return pc.conn }

func (p *openAIWSConnPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, conns := range idle {
		for _, pc := range conns {
			pc.idleTimer.Stop()
			_ = pc.conn.Close()
		}
	}
}

var errOpenAIWSPoolClosed = errors.New("openai ws pool closed")
