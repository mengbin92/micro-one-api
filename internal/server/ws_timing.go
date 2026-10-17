package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
)

type wsTimingKey struct{}
type wsTimedTurn struct {
	ctx         context.Context
	recorder    *requesttrace.Recorder
	responseID  string
	endUpstream func(error)
}
type wsTimingSession struct {
	mu           sync.Mutex
	base         context.Context
	exporter     *requesttrace.Exporter
	turns        []*wsTimedTurn
	byID         map[string]*wsTimedTurn
	completed    []string
	firstSend    bool
	userID       int64
	root         string
	connectionID string
	saturated    bool
	accepted     bool
	acceptStart  time.Time
	acceptEnd    time.Time
	firstFrameAt time.Time
}

func wsTimings(ctx context.Context) *wsTimingSession {
	s, _ := ctx.Value(wsTimingKey{}).(*wsTimingSession)
	return s
}
func (s *HTTPServer) startWSTimings(ctx context.Context, payload []byte) (context.Context, *wsTimingSession) {
	var frame struct {
		Type string `json:"type"`
	}
	if jsonx.Unmarshal(payload, &frame) != nil || frame.Type != "response.create" {
		return ctx, nil
	}
	if s.timingExporter == nil {
		return ctx, nil
	}
	session := &wsTimingSession{exporter: s.timingExporter, byID: map[string]*wsTimedTurn{}, firstSend: true, connectionID: uuid.NewString()}
	session.base = context.WithValue(ctx, wsTimingKey{}, session)
	turn := session.begin()
	return turn.ctx, session
}
func (s *wsTimingSession) begin() *wsTimedTurn {
	ctx, recorder := requesttrace.StartObservation(s.base, "ws_turn", "/v1/responses", "websocket", "legacy", true)
	recorder.Update(func(summary *requesttrace.TimingSummary) { summary.ConnectionID = s.connectionID })
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("connection.id", s.connectionID))
	recorder.Update(func(summary *requesttrace.TimingSummary) { summary.Outcome = "rejected" })
	requesttrace.BindUser(ctx, s.userID)
	if s.firstSend {
		requesttrace.BindRoot(ctx, s.root)
	}
	turn := &wsTimedTurn{ctx: ctx, recorder: recorder}
	s.turns = append(s.turns, turn)
	return turn
}

// ConnectionMilestones records the handshake and first-frame wait measured by
// the forwarder before the session existed. They are attached as backdated
// stages to the first turn's observation, per the design rule that the
// connection phases are charged to the turn in which they actually happen.
func (s *wsTimingSession) ConnectionMilestones(acceptStart, acceptEnd, firstFrameAt time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accepted || acceptStart.IsZero() || acceptEnd.Before(acceptStart) || firstFrameAt.Before(acceptEnd) {
		return
	}
	s.accepted = true
	s.acceptStart, s.acceptEnd, s.firstFrameAt = acceptStart, acceptEnd, firstFrameAt
	if len(s.turns) > 0 {
		s.attachConnectionStages(s.turns[0])
	}
}

func (s *wsTimingSession) attachConnectionStages(turn *wsTimedTurn) {
	if !s.accepted {
		return
	}
	turn.recorder.AddElapsedStage("ws.accept", s.acceptStart, s.acceptEnd, nil)
	turn.recorder.AddElapsedStage("ws.first_frame", s.acceptEnd, s.firstFrameAt, nil)
}

func (s *wsTimingSession) Identity(userID int64, root string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userID, s.root = userID, root
	for _, turn := range s.turns {
		requesttrace.BindUser(turn.ctx, userID)
		requesttrace.BindRoot(turn.ctx, root)
	}
}
func (s *wsTimingSession) Send(ctx context.Context, payload []byte) context.Context {
	return s.send(ctx, payload, false)
}

// SendFirst is the first inbound frame replayed by the connection failover
// loop. Replaying it starts another attempt, never another client turn.
func (s *wsTimingSession) SendFirst(ctx context.Context, payload []byte) context.Context {
	return s.send(ctx, payload, true)
}

func (s *wsTimingSession) send(ctx context.Context, payload []byte, first bool) context.Context {
	if s == nil {
		return ctx
	}
	var frame struct {
		Type string `json:"type"`
	}
	if jsonx.Unmarshal(payload, &frame) != nil || frame.Type != "response.create" {
		return ctx
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saturated || len(s.turns) >= 64 {
		s.saturated = true
		s.exporter.DropTurn()
		for _, turn := range s.turns {
			turn.recorder.Missing("ws_turn_capacity")
		}
		return s.base
	}
	var turn *wsTimedTurn
	if (s.firstSend || first) && len(s.turns) > 0 {
		turn = s.turns[0]
		s.firstSend = false
		if first && turn.endUpstream != nil {
			turn.endUpstream(errors.New("upstream_retry"))
			turn.endUpstream = nil
		}
	} else {
		if len(s.turns) > 0 {
			for _, pending := range s.turns {
				pending.recorder.Missing("concurrent_ws_turns")
			}
		}
		turn = s.begin()
		if len(s.turns) > 1 {
			turn.recorder.Missing("concurrent_ws_turns")
		}
		_, end := requesttrace.Phase(turn.ctx, "relay.plan")
		trace.SpanFromContext(turn.ctx).SetAttributes(attribute.Bool("routing.reused", true))
		end(nil)
	}
	if first && requesttrace.FromContext(ctx).Number > 0 {
		turn.ctx = requesttrace.WithAttempt(turn.ctx, requesttrace.FromContext(ctx))
	} else {
		turn.ctx = requesttrace.EnsureAttempt(turn.ctx, 1)
	}
	if turn.endUpstream == nil {
		var upstreamCtx context.Context
		upstreamCtx, turn.endUpstream = requesttrace.Phase(turn.ctx, "response.stream")
		turn.ctx = upstreamCtx
	}
	return turn.ctx
}
func (s *wsTimingSession) turn(id string) *wsTimedTurn {
	if id != "" {
		if turn := s.byID[id]; turn != nil {
			return turn
		}
		for _, old := range s.completed {
			if old == id {
				return nil
			}
		}
	}
	for _, turn := range s.turns {
		if turn.responseID == "" {
			turn.responseID = id
			if id != "" {
				s.byID[id] = turn
			}
			return turn
		}
	}
	if len(s.turns) > 0 && id == "" {
		return s.turns[0]
	}
	return nil
}
func (s *wsTimingSession) Context(id string, fallback context.Context) context.Context {
	if s == nil {
		return fallback
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn := s.turn(id); turn != nil {
		return turn.ctx
	}
	return fallback
}
func (s *wsTimingSession) Terminal(id string, failed bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn := s.turn(id); turn != nil {
		turn.recorder.UpstreamTerminal(failed)
		if turn.endUpstream != nil {
			var err error
			if failed {
				err = errors.New("upstream_failed")
			}
			turn.endUpstream(err)
			turn.endUpstream = nil
		}
	}
}
func (s *wsTimingSession) Written(id string, payload []byte, terminal bool, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turn(id)
	if turn == nil {
		return
	}
	if err == nil {
		turn.recorder.Write(len(payload), nil)
		probe := requesttrace.ContentProbe{}
		probe.Frame(payload)
		if probe.Truncated {
			turn.recorder.Missing("output_frame_limit")
		}
		if probe.Kind != "" {
			turn.recorder.Content(probe.Kind)
		}
		if terminal {
			turn.recorder.TerminalOutcome(probe.TerminalOutcome())
		}
	} else {
		turn.recorder.Write(0, err)
	}
	if !terminal && err == nil {
		return
	}
	if turn.endUpstream != nil {
		turn.endUpstream(err)
		turn.endUpstream = nil
	}
	outcome := "success"
	if err != nil {
		outcome = "incomplete"
	}
	s.exporter.Submit(turn.ctx, turn.recorder.Finish(outcome))
	delete(s.byID, id)
	if id != "" {
		s.completed = append(s.completed, id)
		if len(s.completed) > 64 {
			s.completed = s.completed[1:]
		}
	}
	for i, value := range s.turns {
		if value == turn {
			s.turns = append(s.turns[:i], s.turns[i+1:]...)
			break
		}
	}
}
func (s *wsTimingSession) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, turn := range s.turns {
		if turn.endUpstream != nil {
			turn.endUpstream(context.Canceled)
		}
		s.exporter.Submit(turn.ctx, turn.recorder.Finish("incomplete"))
	}
	s.turns = nil
	s.byID = map[string]*wsTimedTurn{}
}

func (s *wsTimingSession) Sent(ctx context.Context, err error) {
	if s == nil {
		return
	}
	requesttrace.Observation(ctx).Update(func(summary *requesttrace.TimingSummary) {
		if err == nil {
			summary.Outcome = "unknown"
		} else {
			summary.Outcome = "failed"
		}
	})
}

func (s *wsTimingSession) Interrupted(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, turn := range s.turns {
		turn.recorder.Update(func(summary *requesttrace.TimingSummary) {
			if err != nil && requesttrace.Outcome(err) == "cancelled" {
				summary.Outcome = "cancelled"
			} else if summary.Outcome == "unknown" {
				summary.Outcome = "incomplete"
			}
		})
	}
}
