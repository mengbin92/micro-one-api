package evidence

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Record is observation evidence, never a receipt for financial settlement.
type Record struct {
	Observation      Observation   `json:"observation"`
	Execution        string        `json:"execution"`
	HTTPStatus       int           `json:"http_status,omitempty"`
	Stream           bool          `json:"stream"`
	Complete         bool          `json:"complete"`
	Events           []Observation `json:"events,omitempty"`
	EventCount       int           `json:"event_count,omitempty"`
	DroppedEvents    int           `json:"dropped_events,omitempty"`
	UpstreamEndpoint string        `json:"upstream_endpoint,omitempty"`
	APIVersion       string        `json:"api_version,omitempty"`
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
	resp, err := client.Do(req) // #nosec G704 -- caller owns the existing URL/SSRF validation.
	if req == nil {
		return resp, err
	}
	recorder := RecorderFromContext(req.Context())
	if recorder == nil {
		return resp, err
	}
	if err != nil {
		recorder(Record{Observation: Observation{Protocol: protocol, ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "missing", Reason: "transport_error"}, Execution: "execution_unknown", UpstreamEndpoint: req.URL.EscapedPath(), APIVersion: apiVersion(req)})
		return resp, err
	}
	stream := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	resp.Body = &observedBody{ReadCloser: resp.Body, recorder: recorder, protocol: protocol, status: resp.StatusCode, stream: stream, tracker: NewTracker(protocol), providerID: providerRequestID(resp.Header), endpoint: req.URL.EscapedPath(), apiVersion: apiVersion(req)}
	return resp, nil
}

func apiVersion(req *http.Request) string {
	if version := req.Header.Get("Anthropic-Version"); version != "" && len(version) <= 128 {
		return version
	}
	if version := req.URL.Query().Get("api-version"); version != "" && len(version) <= 128 {
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
	io.ReadCloser
	recorder             Recorder
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
	b.mu.Unlock()
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *observedBody) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }

func (b *observedBody) finish() {
	b.once.Do(func() {
		b.mu.Lock()
		b.done = true
		r := Record{HTTPStatus: b.status, Stream: b.stream, Complete: b.eof && !b.readFailed, Execution: "response_received", UpstreamEndpoint: b.endpoint, APIVersion: b.apiVersion}
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
		b.recorder(r)
	})
}
