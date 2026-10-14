package evidence

import (
	"bytes"
	"strings"

	"micro-one-api/pkg/jsonx"
)

const MaxEvents = 32

// Tracker merges cumulative fields by presence. Explicit zero overwrites a
// prior snapshot; message_start and message_delta can supply different fields.
type Tracker struct {
	candidateParser func([]byte) Observation
	protocol        string
	usage           map[string]any
	pending         []byte
	discardLine     bool
	data            []byte
	latest          Observation
	fault           *Observation
	success, failed bool
	Events          []Observation
	EventCount      int
	DroppedEvents   int
}

func NewTracker(protocol string) *Tracker {
	return &Tracker{protocol: protocol, usage: map[string]any{}}
}

func (t *Tracker) ObserveFrame(body []byte) {
	o := t.parse(body)
	if o.Terminal == "completed" {
		t.success = true
	}
	if o.Terminal == "failed" {
		t.failed = true
	}
	if o.Status == "missing" && o.Reason == "usage_absent" && o.Terminal == "" {
		return
	}
	t.EventCount++
	if len(t.Events) < MaxEvents {
		t.Events = append(t.Events, o)
	} else {
		t.DroppedEvents++
	}
	if o.Status == "missing" && o.Reason == "usage_absent" {
		return
	}
	if o.Status == "invalid" || o.Status == "unobserved" {
		if t.fault == nil {
			t.fault = &o
		}
		return
	}
	if t.fault != nil {
		return
	}
	if o.Usage != nil {
		var m map[string]any
		d := jsonx.NewDecoder(bytes.NewReader(o.Usage))
		d.UseNumber()
		if err := d.Decode(&m); err == nil {
			mergeSnapshot(t.usage, m)
		}
	}
	key := "usage"
	if t.protocol == "gemini" {
		key = "usageMetadata"
	}
	combined, _ := jsonx.Marshal(map[string]any{key: t.usage})
	if len(combined) > MaxUsageBytes {
		t.limitFault()
		t.usage = nil
		return
	}
	providerID := t.latest.ProviderRequestID
	if o.ProviderRequestID != "" {
		providerID = o.ProviderRequestID
	}
	t.latest = t.parse(combined)
	t.latest.ProviderRequestID = providerID
}

func mergeSnapshot(dst, src map[string]any) {
	for k, value := range src {
		if nested, ok := value.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				mergeSnapshot(existing, nested)
				continue
			}
		}
		dst[k] = value
	}
}

// Write observes complete SSE events and bounds both an unfinished line and
// an unfinished event. It always consumes p; this observer never blocks data.
func (t *Tracker) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		line, rest, found := bytes.Cut(p, []byte{'\n'})
		if len(t.pending)+len(line) > MaxFrameBytes {
			t.discardLine = true
			t.pending = nil
			t.limitFault()
		}
		if !t.discardLine {
			t.pending = append(t.pending, line...)
		}
		if !found {
			break
		}
		if !t.discardLine {
			t.observeLine(strings.TrimSuffix(string(t.pending), "\r"))
		}
		t.pending = nil
		t.discardLine = false
		p = rest
	}
	return n, nil
}

func (t *Tracker) observeLine(line string) {
	if line == "" {
		t.flushEvent()
		return
	}
	if data, ok := strings.CutPrefix(line, "data:"); ok {
		data = strings.TrimPrefix(data, " ")
		if len(t.data)+len(data)+1 > MaxFrameBytes {
			t.limitFault()
			t.data = nil
			return
		}
		if len(t.data) > 0 {
			t.data = append(t.data, '\n')
		}
		t.data = append(t.data, data...)
	}
}

func (t *Tracker) flushEvent() {
	if bytes.Equal(t.data, []byte("[DONE]")) {
		if t.protocol == "openai_chat" {
			t.success = true
		}
	} else if len(t.data) > 0 {
		t.ObserveFrame(t.data)
	}
	// An event name alone is not proof of a complete, valid terminal payload.
	t.data = nil
}

func (t *Tracker) limitFault() {
	if t.fault == nil {
		o := t.emptyObservation("unobserved", "frame_limit")
		t.fault = &o
	}
}

// Finish doesn't turn a partial SSE event into a completed event.
func (t *Tracker) Finish() (Observation, bool) {
	if len(t.pending) > 0 || len(t.data) > 0 {
		t.success = false
	}
	if t.fault != nil {
		return *t.fault, false
	}
	if t.latest.Status == "" {
		return t.emptyObservation("missing", "usage_absent"), t.success && !t.failed
	}
	return t.latest, t.success && !t.failed
}

func (t *Tracker) Snapshot() Observation {
	if t.fault != nil {
		return *t.fault
	}
	if t.latest.Status != "" {
		return t.latest
	}
	return t.emptyObservation("missing", "usage_absent")
}

func (t *Tracker) emptyObservation(status, reason string) Observation {
	o := Observation{Protocol: t.protocol, ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: status, Reason: reason}
	if t.candidateParser != nil {
		candidate := t.candidateParser([]byte("{}"))
		o.ParserRevision, o.ContractRevision = candidate.ParserRevision, candidate.ContractRevision
	}
	return o
}

func (t *Tracker) parse(body []byte) Observation {
	if t.candidateParser != nil {
		return t.candidateParser(body)
	}
	return Parse(body, t.protocol)
}
