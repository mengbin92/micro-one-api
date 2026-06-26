package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
)

// openAIWSConnPool is a per-channel upstream WebSocket connection pool. It
// caches idle upstream connections keyed by channel ID so that follow-up turns
// (or separate client sessions on the same channel) reuse an established
// connection instead of dialing the upstream again. This mirrors sub2api's
// openAIWSConnPool but is simplified to a per-channel model (no account-level
// concurrency limits or prewarming) which fits micro-one-api's channel routing.
//
// Connections are kept idle in the pool after a relay session ends. The next
// Acquire for the same channel returns the cached conn if it's still alive
// (checked via a non-blocking ping); otherwise a fresh dial occurs. A
// background sweeper evicts connections that have been idle past maxIdle.
type openAIWSConnPool struct {
	dialer             openAIWSUpstreamDialer
	dialTimeout        time.Duration
	maxIdle            time.Duration
	maxConnsPerChannel int

	mu       sync.Mutex
	channels map[int64]*openAIWSChannelPool
	closed   atomic.Bool

	acquireTotal  atomic.Int64
	acquireReuse  atomic.Int64
	acquireCreate atomic.Int64
	acquireFail   atomic.Int64
}

// openAIWSChannelPool holds the idle conns for a single channel.
type openAIWSChannelPool struct {
	mu    sync.Mutex
	conns []*openAIWSPooledConn
}

// openAIWSPooledConn wraps a leased upstream connection with last-used tracking.
type openAIWSPooledConn struct {
	conn       openAIWSFrameConn
	channelID  int64
	lastUsedAt time.Time
	inUse      atomic.Bool
}

const (
	openAIWSPoolDefaultMaxConnsPerChannel = 8
	openAIWSPoolDefaultMaxIdle            = 5 * time.Minute
)

func newOpenAIWSConnPool(dialTimeout time.Duration) *openAIWSConnPool {
	p := &openAIWSConnPool{
		dialer:             newCoderWSUpstreamDialer(),
		dialTimeout:        dialTimeout,
		maxIdle:            openAIWSPoolDefaultMaxIdle,
		maxConnsPerChannel: openAIWSPoolDefaultMaxConnsPerChannel,
		channels:           make(map[int64]*openAIWSChannelPool),
	}
	return p
}

func (p *openAIWSConnPool) getChannelPool(channelID int64) *openAIWSChannelPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp, ok := p.channels[channelID]
	if !ok {
		cp = &openAIWSChannelPool{}
		p.channels[channelID] = cp
	}
	return cp
}

// AcquireOrDial returns a usable upstream connection for the given channel.
// It first checks the pool for a reusable idle connection; if none is
// available it dials a new one. The caller MUST call Release when done (which
// returns the connection to the pool, or closes it if the pool is full/closed).
func (p *openAIWSConnPool) AcquireOrDial(ctx context.Context, channelID int64, wsURL string, headers map[string][]string) (*openAIWSPooledConn, error) {
	if p == nil {
		// Pool disabled: dial directly each time.
		return p.dialNew(ctx, channelID, wsURL, headers)
	}
	p.acquireTotal.Add(1)
	if reused := p.tryReuse(channelID); reused != nil {
		p.acquireReuse.Add(1)
		return reused, nil
	}
	conn, err := p.dialNew(ctx, channelID, wsURL, headers)
	if err != nil {
		p.acquireFail.Add(1)
		return nil, err
	}
	p.acquireCreate.Add(1)
	return conn, nil
}

// tryReuse returns a healthy idle connection from the channel pool, or nil.
func (p *openAIWSConnPool) tryReuse(channelID int64) *openAIWSPooledConn {
	if p.closed.Load() {
		return nil
	}
	cp := p.getChannelPool(channelID)
	cp.mu.Lock()
	defer cp.mu.Unlock()
	now := time.Now()
	for i, pc := range cp.conns {
		if pc == nil {
			continue
		}
		if pc.inUse.Load() {
			continue
		}
		// Evict if idle too long.
		if now.Sub(pc.lastUsedAt) > p.maxIdle {
			_ = pc.conn.Close()
			cp.conns[i] = nil
			continue
		}
		// Health-check: a quick ping. coder/websocket Ping blocks until pong;
		// use a short timeout so a dead conn doesn't stall the pool. We reach
		// the concrete *coderws.Conn because the pool only ever stores
		// *coderWSFrameConn instances (built by the production dialer).
		rawConn := openAIWSConnFromFrameConn(pc.conn)
		if rawConn != nil {
			// Health-check: a quick ping. coder/websocket Ping blocks until
			// pong; use a short timeout so a dead conn doesn't stall the pool.
			pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := rawConn.Ping(pingCtx)
			cancel()
			if err != nil {
				_ = pc.conn.Close()
				cp.conns[i] = nil
				continue
			}
		}
		// If the conn doesn't expose a *coderws.Conn (e.g. a test fake), skip
		// the ping and optimistically reuse — the relay will detect a dead conn
		// via its first read/write error and mark it broken on release.
		pc.inUse.Store(true)
		pc.lastUsedAt = now
		return pc
	}
	// Compact nils.
	cp.compact()
	return nil
}

// dialNew dials a fresh upstream connection and wraps it as a pooled conn.
func (p *openAIWSConnPool) dialNew(ctx context.Context, channelID int64, wsURL string, headers map[string][]string) (*openAIWSPooledConn, error) {
	dialer := p.dialer
	if dialer == nil {
		dialer = newCoderWSUpstreamDialer()
	}
	dialCtx, cancel := context.WithTimeout(ctx, p.dialTimeout)
	defer cancel()
	hdr := make(map[string][]string, len(headers))
	for k, v := range headers {
		hdr[k] = v
	}
	conn, _, _, err := dialer.Dial(dialCtx, wsURL, hdr)
	if err != nil {
		return nil, err
	}
	pc := &openAIWSPooledConn{
		conn:       conn,
		channelID:  channelID,
		lastUsedAt: time.Now(),
	}
	pc.inUse.Store(true)
	return pc, nil
}

// Release returns a connection to the pool for reuse, or closes it if the
// pool is full, closed, or the connection is marked broken (broken=true).
func (p *openAIWSConnPool) Release(pc *openAIWSPooledConn, broken bool) {
	if p == nil || pc == nil {
		return
	}
	pc.inUse.Store(false)
	if broken || p.closed.Load() {
		_ = pc.conn.Close()
		return
	}
	pc.lastUsedAt = time.Now()
	cp := p.getChannelPool(pc.channelID)
	cp.mu.Lock()
	if len(cp.conns) >= p.maxConnsPerChannel {
		cp.mu.Unlock()
		_ = pc.conn.Close()
		return
	}
	cp.conns = append(cp.conns, pc)
	cp.mu.Unlock()
}

// ConnID returns a stable identifier for logging.
func (pc *openAIWSPooledConn) ConnID() string {
	return pc.conn.(interface{ String() string }).String()
}

// FrameConn returns the underlying frame connection for relay use.
func (pc *openAIWSPooledConn) FrameConn() openAIWSFrameConn { return pc.conn }

// Close shuts down the pool and closes all idle connections.
func (p *openAIWSConnPool) Close() {
	if p == nil {
		return
	}
	p.closed.Store(true)
	p.mu.Lock()
	channels := p.channels
	p.channels = make(map[int64]*openAIWSChannelPool)
	p.mu.Unlock()
	for _, cp := range channels {
		cp.mu.Lock()
		for _, pc := range cp.conns {
			if pc != nil {
				_ = pc.conn.Close()
			}
		}
		cp.conns = nil
		cp.mu.Unlock()
	}
}

func (cp *openAIWSChannelPool) compact() {
	written := 0
	for _, pc := range cp.conns {
		if pc != nil {
			cp.conns[written] = pc
			written++
		}
	}
	for i := written; i < len(cp.conns); i++ {
		cp.conns[i] = nil
	}
	cp.conns = cp.conns[:written]
}

// errOpenAIWSPoolClosed is returned by operations on a closed pool.
var errOpenAIWSPoolClosed = errors.New("openai ws pool closed")

// openAIWSConnFromFrameConn extracts the underlying *coderws.Conn from a
// *coderWSFrameConn, returning nil for any other FrameConn implementation.
func openAIWSConnFromFrameConn(conn openAIWSFrameConn) *coderws.Conn {
	if c, ok := conn.(*coderWSFrameConn); ok && c != nil {
		return c.conn
	}
	return nil
}
