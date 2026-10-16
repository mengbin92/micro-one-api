package evidence

import (
	"context"
	"io"
	"micro-one-api/domain/upstream/contract"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Record is observation evidence, never a receipt for financial settlement.
type Record struct {
	ParameterContract *contract.Trace `json:"parameter_contract,omitempty"`
	StartedAtUnixNano int64           `json:"started_at_unix_nano,omitempty"`
	Observation       Observation     `json:"observation"`
	Execution         string          `json:"execution"`
	HTTPStatus        int             `json:"http_status,omitempty"`
	Stream            bool            `json:"stream"`
	Complete          bool            `json:"complete"`
	Events            []Observation   `json:"events,omitempty"`
	EventCount        int             `json:"event_count,omitempty"`
	DroppedEvents     int             `json:"dropped_events,omitempty"`
	UpstreamEndpoint  string          `json:"upstream_endpoint,omitempty"`
	APIVersion        string          `json:"api_version,omitempty"`
}

type durableKey struct{}
type durableRecorder struct {
	Before func() error
	Record func(Record) error
}

func WithDurableRecorder(ctx context.Context, before func() error, record func(Record) error) context.Context {
	return context.WithValue(ctx, durableKey{}, durableRecorder{before, record})
}
func CheckDurable(ctx context.Context) error {
	r, _ := ctx.Value(durableKey{}).(durableRecorder)
	if r.Before != nil {
		return r.Before()
	}
	return nil
}
func RecordDurable(ctx context.Context, record Record) error {
	r, _ := ctx.Value(durableKey{}).(durableRecorder)
	if r.Record != nil {
		return r.Record(record)
	}
	return nil
}

type recorderKey struct{}
type Recorder func(Record)

func ProtocolForPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/chat/completions"), strings.HasSuffix(path, "/completions"):
		return "openai_chat"
	case strings.HasSuffix(path, "/responses"):
		return "responses"
	case strings.HasSuffix(path, "/messages"):
		return "anthropic_messages"
	case strings.HasSuffix(path, ":generateContent"), strings.HasSuffix(path, ":streamGenerateContent"):
		return "gemini"
	case strings.HasSuffix(path, "/embeddings"):
		return "embeddings"
	default:
		return "other"
	}
}

func WithRecorder(ctx context.Context, recorder Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}
func RecorderFromContext(ctx context.Context) Recorder {
	recorder, _ := ctx.Value(recorderKey{}).(Recorder)
	return recorder
}

// Do captures the original response before any provider/adaptor projection.
// Without a recorder it is the original client call with no body overhead.
func Do(client *http.Client, req *http.Request, protocol string) (*http.Response, error) {
	var parameterTrace *contract.Trace
	if req != nil {
		var err error
		req, parameterTrace, err = contract.PrepareHTTP(req, protocol)
		if err != nil {
			return nil, &contract.Rejection{Err: err}
		}
	}
	if req != nil {
		if err := CheckDurable(req.Context()); err != nil {
			return nil, err
		}
		if HasDurableRecorder(req.Context()) {
			// http.Client redirects happen inside Do and would bypass the
			// one-send admission and procurement identity. Preserve the redirect
			// response as evidence without mutating the shared storage client.
			copy := *client
			copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			client = &copy
		}
	}
	startedAt := int64(0)
	if parameterTrace != nil || os.Getenv("RELAY_UPSTREAM_EXECUTION_TIME_V1") == "true" {
		startedAt = time.Now().UnixNano()
	}
	resp, err := client.Do(req) // #nosec G704 -- caller owns the existing URL/SSRF validation.
	if req == nil {
		return resp, err
	}
	recorder := RecorderFromContext(req.Context())
	durable, _ := req.Context().Value(durableKey{}).(durableRecorder)
	contractReporter := contractReporterFrom(req.Context())
	if recorder == nil && durable.Record == nil && parameterTrace == nil {
		return resp, err
	}
	if err != nil {
		record := Record{ParameterContract: parameterTrace, StartedAtUnixNano: startedAt, Observation: Observation{Protocol: protocol, ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "missing", Reason: "transport_error"}, Execution: "execution_unknown", UpstreamEndpoint: req.URL.EscapedPath(), APIVersion: apiVersion(req)}
		if recorder != nil {
			recorder(record)
		}
		if durable.Record != nil {
			if failure := durable.Record(record); failure != nil {
				return resp, failure
			}
		}
		return resp, err
	}
	stream := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	if durable.Record != nil {
		header := Record{ParameterContract: parameterTrace, StartedAtUnixNano: startedAt, Observation: Observation{Protocol: protocol, ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "missing", Reason: "response_headers_only", ProviderRequestID: providerRequestID(resp.Header)}, Execution: "checkpoint", HTTPStatus: resp.StatusCode, Stream: stream, UpstreamEndpoint: req.URL.EscapedPath(), APIVersion: apiVersion(req)}
		if err := durable.Record(header); err != nil {
			_ = resp.Body.Close()
			// The supplier has already been called. Even if a header checkpoint
			// cannot be saved, use the protected terminal slot to retain its
			// identity and incomplete result; never fabricate missing usage.
			header.Execution, header.Complete = "incomplete", false
			header.Observation.Reason = "header_checkpoint_failed"
			_ = durable.Record(header)
			return nil, err
		}
	}
	resp.Body = &observedBody{contractReporter: contractReporter, parameterTrace: parameterTrace, startedAt: startedAt, ReadCloser: resp.Body, recorder: recorder, durable: durable.Record, protocol: protocol, status: resp.StatusCode, stream: stream, tracker: NewTracker(protocol), providerID: providerRequestID(resp.Header), endpoint: req.URL.EscapedPath(), apiVersion: apiVersion(req)}
	return resp, nil
}

func apiVersion(req *http.Request) string {
	if version := req.Header.Get("Anthropic-Version"); version != "" && len(version) <= 128 {
		return version
	}
	if version := req.URL.Query().Get("api-version"); version != "" && len(version) <= 128 {
		return version
	}
	if version := contract.GeminiAPIVersion(req.URL.Path); ProtocolForPath(req.URL.Path) == "gemini" && version != "" && len(version) <= 128 {
		return version
	}
	if strings.HasPrefix(req.URL.Path, "/v1beta/") {
		return "v1beta"
	}
	return "unknown"
}

func providerRequestID(header http.Header) string {
	for _, key := range []string{"x-request-id", "request-id", "x-goog-request-id"} {
		if id := header.Get(key); id != "" && len(id) <= 256 {
			return id
		}
	}
	return ""
}

type observedBody struct {
	contractReporter func(Record)
	parameterTrace   *contract.Trace
	startedAt        int64
	checkpointEvents int
	io.ReadCloser
	recorder             Recorder
	durable              func(Record) error
	persistErr           error
	protocol             string
	status               int
	stream               bool
	tracker              *Tracker
	body                 []byte
	tooLarge             bool
	eof                  bool
	readFailed           bool
	providerID           string
	once                 sync.Once
	mu                   sync.Mutex
	done                 bool
	endpoint, apiVersion string
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return n, err
	}
	if n > 0 {
		if b.stream {
			_, _ = b.tracker.Write(p[:n])
		} else if !b.tooLarge {
			if len(b.body)+n > MaxFrameBytes {
				b.tooLarge = true
				b.body = nil
			} else {
				b.body = append(b.body, p[:n]...)
			}
		}
	}
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.readFailed = true
	}
	var checkpoint *Record
	if b.durable != nil && b.stream && b.tracker.EventCount > b.checkpointEvents && b.checkpointEvents < MaxEvents && err == nil {
		b.checkpointEvents = b.tracker.EventCount
		checkpoint = &Record{ParameterContract: b.parameterTrace, StartedAtUnixNano: b.startedAt, Observation: b.tracker.Snapshot(), Execution: "checkpoint", HTTPStatus: b.status, Stream: true, Events: append([]Observation(nil), b.tracker.Events...), EventCount: b.tracker.EventCount, DroppedEvents: b.tracker.DroppedEvents, UpstreamEndpoint: b.endpoint, APIVersion: b.apiVersion}
	}
	b.mu.Unlock()
	if checkpoint != nil {
		if failure := b.durable(*checkpoint); failure != nil {
			return n, failure
		}
	}
	if err != nil {
		b.finish()
		if b.persistErr != nil {
			return n, b.persistErr
		}
	}
	return n, err
}

func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish()
	if b.persistErr != nil {
		return b.persistErr
	}
	return err
}

func (b *observedBody) finish() {
	b.once.Do(func() {
		b.mu.Lock()
		b.done = true
		r := Record{ParameterContract: b.parameterTrace, StartedAtUnixNano: b.startedAt, HTTPStatus: b.status, Stream: b.stream, Complete: b.eof && !b.readFailed, Execution: "response_received", UpstreamEndpoint: b.endpoint, APIVersion: b.apiVersion}
		if b.stream {
			var terminal bool
			r.Observation, terminal = b.tracker.Finish()
			r.Complete = r.Complete && terminal
			r.Events, r.EventCount, r.DroppedEvents = b.tracker.Events, b.tracker.EventCount, b.tracker.DroppedEvents
		} else if b.tooLarge {
			r.Observation = Observation{Protocol: b.protocol, ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "unobserved", Reason: "frame_limit"}
		} else {
			r.Observation = Parse(b.body, b.protocol)
		}
		if r.Observation.Terminal == "failed" {
			r.Complete = false
		}
		if r.Observation.ProviderRequestID == "" {
			r.Observation.ProviderRequestID = b.providerID
		}
		if !r.Complete {
			r.Execution = "incomplete"
		}
		if b.status < 200 || b.status >= 300 {
			r.Execution = "upstream_rejected"
		}
		b.body = nil
		b.mu.Unlock()
		if b.durable != nil {
			b.persistErr = b.durable(r)
		}
		if b.recorder != nil {
			b.recorder(r)
		}
		if b.contractReporter != nil && r.ParameterContract != nil {
			b.contractReporter(r)
		}
	})
}

func HasDurableRecorder(ctx context.Context) bool {
	r, _ := ctx.Value(durableKey{}).(durableRecorder)
	return r.Record != nil
}

type observationReaderKey struct{}

func WithObservationReader(ctx context.Context, read func() *Record) context.Context {
	return context.WithValue(ctx, observationReaderKey{}, read)
}
func NativeObservation(ctx context.Context) (*Record, bool) {
	read, ok := ctx.Value(observationReaderKey{}).(func() *Record)
	if !ok {
		return nil, false
	}
	return read(), true
}
func ContractSnapshots(ctx context.Context) []contract.Snapshot { return contract.Snapshots(ctx) }

type contractReporterKey struct{}

func WithContractReporter(ctx context.Context, report func(Record)) context.Context {
	return context.WithValue(ctx, contractReporterKey{}, report)
}
func contractReporterFrom(ctx context.Context) func(Record) {
	f, _ := ctx.Value(contractReporterKey{}).(func(Record))
	return f
}
