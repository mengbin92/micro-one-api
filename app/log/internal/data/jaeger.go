package data

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
)

// Data holds the owner-owned long-lived query client. Browser input never
// selects an upstream address or arbitrary trace ID.
type Data struct {
	traceHTTP     *http.Client
	traceEndpoint string
}

func newTraceData() *Data {
	endpoint := strings.TrimRight(os.Getenv("JAEGER_QUERY_ENDPOINT"), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		endpoint = ""
	}
	return &Data{traceHTTP: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, traceEndpoint: endpoint}
}

type otlpAttribute struct {
	Key   string `json:"key"`
	Value struct {
		String string   `json:"stringValue"`
		Int    string   `json:"intValue"`
		Bool   *bool    `json:"boolValue"`
		Double *float64 `json:"doubleValue"`
	} `json:"value"`
}
type otlpSpan struct {
	Kind       jsonx.RawMessage `json:"kind"`
	TraceID    string           `json:"traceId"`
	SpanID     string           `json:"spanId"`
	ParentID   string           `json:"parentSpanId"`
	Name       string           `json:"name"`
	Start      string           `json:"startTimeUnixNano"`
	End        string           `json:"endTimeUnixNano"`
	Attributes []otlpAttribute  `json:"attributes"`
	Status     struct {
		Code jsonx.RawMessage `json:"code"`
	} `json:"status"`
	Events []struct {
		Name       string          `json:"name"`
		Time       string          `json:"timeUnixNano"`
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"events"`
}
type traceEnvelope struct {
	Result struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []otlpAttribute `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []otlpSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	} `json:"result"`
	Error jsonx.RawMessage `json:"error"`
}

func (r *Repository) ReadRequestTrace(ctx context.Context, summary *requesttrace.TimingSummary) (*requesttrace.TraceDetail, error) {
	now := time.Now()
	detail := &requesttrace.TraceDetail{Summary: summary, DetailState: "not_recorded", ObservedAtMS: now.UnixMilli(), DetailRetentionHours: 48, Spans: []requesttrace.TraceSpan{}, Milestones: []requesttrace.TraceMilestone{}}
	if summary.OTelTraceID == "" || summary.CollectionState == "not_recorded" {
		return detail, nil
	}
	detail.DetailState = "unavailable"
	if r.data == nil || r.data.traceEndpoint == "" {
		detail.MissingReasons = []string{"query_not_configured"}
		return detail, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	budget := &traceBudget{bytes: 2 << 20, spans: 2000}
	spans, truncated, err := r.fetchTrace(ctx, summary.OTelTraceID, budget)
	detail.Truncated = truncated
	if err != nil {
		if errors.Is(err, errTraceMissing) {
			return missingTrace(detail, now), nil
		}
		detail.MissingReasons = []string{"query_failed"}
		return detail, nil
	}
	if len(spans) == 0 {
		if truncated {
			detail.DetailState = "partial"
			detail.MissingReasons = []string{"response_limit"}
			return detail, nil
		}
		return missingTrace(detail, now), nil
	}
	// Only the owned observation root and its descendants can be returned.
	// Shared batches must be followed by explicit safe references, not links.
	allowed := map[string]bool{summary.SpanID: true}
	for pass := 0; pass < len(spans); pass++ {
		changed := false
		for _, span := range spans {
			id, parent := traceIDHex(span.span.SpanID, 8), traceIDHex(span.span.ParentID, 8)
			if traceIDHex(span.span.TraceID, 16) == summary.OTelTraceID && allowed[parent] && !allowed[id] {
				allowed[id] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	rootStart := int64(0)
	for _, span := range spans {
		if traceIDHex(span.span.SpanID, 8) == summary.SpanID {
			rootStart, _ = strconv.ParseInt(span.span.Start, 10, 64)
		}
	}
	if rootStart == 0 {
		return missingTrace(detail, now), nil
	}
	phaseCount := map[string]int{}
	serverParents := map[string]bool{}
	for _, located := range spans {
		kind := string(located.span.Kind)
		if kind == "2" || strings.Contains(kind, "SERVER") {
			serverParents[traceIDHex(located.span.ParentID, 8)] = true
		}
	}
	for _, located := range spans {
		span := located.span
		id := traceIDHex(span.SpanID, 8)
		if !allowed[id] || traceIDHex(span.TraceID, 16) != summary.OTelTraceID {
			continue
		}
		start, _ := strconv.ParseInt(span.Start, 10, 64)
		end, _ := strconv.ParseInt(span.End, 10, 64)
		if start <= 0 || end < start {
			detail.MissingReasons = append(detail.MissingReasons, "invalid_span_clock")
			continue
		}
		item := requesttrace.TraceSpan{ID: id, ParentID: traceIDHex(span.ParentID, 8), Service: safeTraceService(located.service), Phase: safeTracePhase(span.Name),
			StartOffsetMS: float64(start-rootStart) / 1e6, DurationMS: float64(end-start) / 1e6, Outcome: "unknown", SafeAttributes: map[string]string{}}
		if item.StartOffsetMS < 0 {
			detail.MissingReasons = appendReason(detail.MissingReasons, "clock_skew")
		}
		for _, a := range span.Attributes {
			value := a.Value.String
			if value == "" {
				value = a.Value.Int
			}
			if value == "" && a.Value.Bool != nil {
				value = strconv.FormatBool(*a.Value.Bool)
			}
			if value == "" && a.Value.Double != nil {
				value = strconv.FormatFloat(*a.Value.Double, 'f', 3, 64)
			}
			switch a.Key {
			case "outcome":
				if requesttrace.ValidTimingOutcome(value) {
					item.Outcome = value
				}
			case "relay.attempt.number":
				n, _ := strconv.ParseInt(value, 10, 32)
				item.AttemptNumber = int32(n)
			case "rpc.system", "rpc.method", "rpc.status", "network.connection.reused", "network.connection.idle", "http.response.status_code", "cache.hit", "db.operation.name", "upstream.protocol", "queue.wait_ms", "batch.shared", "telemetry.carrier.valid":
				if safe, ok := safeTraceAttribute(a.Key, value); ok {
					item.SafeAttributes[a.Key] = safe
				}
			}
		}
		if string(span.Status.Code) == "2" || strings.Contains(string(span.Status.Code), "ERROR") {
			item.Outcome = "failed"
		}
		if kind := string(span.Kind); kind == "3" || strings.Contains(kind, "CLIENT") {
			for _, a := range span.Attributes {
				if a.Key == "rpc.system" && a.Value.String == "grpc" && !serverParents[id] {
					detail.MissingReasons = appendReason(detail.MissingReasons, "missing_rpc_server_spans")
				}
			}
		}
		if item.Phase == "unclassified" {
			detail.MissingReasons = appendReason(detail.MissingReasons, "unclassified_phase")
		}
		phaseCount[span.Name]++
		detail.Spans = append(detail.Spans, item)
		for _, event := range span.Events {
			switch event.Name {
			case "output.first_content", "upstream.first_content", "upstream.first_byte", "upstream.first_body", "upstream.terminal", "output.finished", "handler.finished", "output.terminal", "turn.finished", "rpc.finished":
			default:
				continue
			}
			timestamp, _ := strconv.ParseInt(event.Time, 10, 64)
			kind := ""
			for _, a := range event.Attributes {
				if a.Key == "output.kind" {
					switch a.Value.String {
					case "text", "reasoning", "tool_call":
						kind = a.Value.String
					}
				}
			}
			detail.Milestones = append(detail.Milestones, requesttrace.TraceMilestone{Kind: event.Name, OffsetMS: float64(timestamp-rootStart) / 1e6, OutputKind: kind})
		}
	}
	seenRelated := map[string]bool{}
	for _, located := range spans {
		span := located.span
		if !allowed[traceIDHex(span.SpanID, 8)] {
			continue
		}
		var batchTrace, batchSpan string
		for _, a := range span.Attributes {
			if a.Key == "batch.trace_id" {
				batchTrace = traceIDHex(a.Value.String, 16)
			}
			if a.Key == "batch.span_id" {
				batchSpan = traceIDHex(a.Value.String, 8)
			}
		}
		if batchTrace == "" || batchSpan == "" || seenRelated[batchTrace+batchSpan] {
			continue
		}
		if len(seenRelated) == 8 {
			detail.Truncated = true
			detail.MissingReasons = appendReason(detail.MissingReasons, "related_operation_limit")
			break
		}
		seenRelated[batchTrace+batchSpan] = true
		batches, cut, err := r.fetchTrace(ctx, batchTrace, budget)
		if cut {
			detail.Truncated = true
		}
		found := false
		if err == nil {
			for _, batch := range batches {
				if batch.span.Name != "log.flush" || traceIDHex(batch.span.SpanID, 8) != batchSpan {
					continue
				}
				started, _ := strconv.ParseInt(batch.span.Start, 10, 64)
				ended, _ := strconv.ParseInt(batch.span.End, 10, 64)
				if started <= 0 || ended < started {
					continue
				}
				found = true
				outcome := "unknown"
				for _, a := range batch.span.Attributes {
					if a.Key == "outcome" && requesttrace.ValidTimingOutcome(a.Value.String) {
						outcome = a.Value.String
					}
				}
				detail.RelatedOperations = append(detail.RelatedOperations, requesttrace.RelatedOperation{TraceID: batchTrace, SpanID: batchSpan, Phase: "log.flush", StartOffsetMS: float64(started-rootStart) / 1e6, DurationMS: float64(ended-started) / 1e6, Outcome: outcome, Shared: true})
			}
		}
		if !found {
			detail.MissingReasons = appendReason(detail.MissingReasons, "shared_batch_pending")
		}
	}
	sort.SliceStable(detail.Spans, func(i, j int) bool { return detail.Spans[i].StartOffsetMS < detail.Spans[j].StartOffsetMS })
	detail.DetailState = "available"
	for _, stage := range summary.Stages {
		if phaseCount[stage.Phase] < int(stage.Count) {
			detail.MissingReasons = appendReason(detail.MissingReasons, "missing_"+strings.ReplaceAll(stage.Phase, ".", "_"))
		}
	}
	if len(detail.Spans) <= 1 {
		detail.MissingReasons = appendReason(detail.MissingReasons, "missing_child_spans")
	}
	if phaseCount["relay.attempt"] < len(summary.Attempts) {
		detail.MissingReasons = appendReason(detail.MissingReasons, "missing_attempt_spans")
	}
	if snapshot := summary.SettlementSnapshot; snapshot != nil && snapshot.Status == "enqueued" && phaseCount["settlement.worker"] == 0 {
		detail.MissingReasons = appendReason(detail.MissingReasons, "settlement_worker_pending")
	}
	if summary.CollectionState == "partial" {
		detail.MissingReasons = appendReason(detail.MissingReasons, "collection_partial")
	}
	if detail.Truncated {
		detail.MissingReasons = appendReason(detail.MissingReasons, "span_limit")
	}
	if len(detail.MissingReasons) > 0 {
		detail.DetailState = "partial"
		if now.Sub(time.UnixMilli(summary.StartedAtMS)) < 30*time.Second && !detail.Truncated && summary.CollectionState != "partial" {
			for _, reason := range detail.MissingReasons {
				if strings.HasPrefix(reason, "missing_") || strings.HasSuffix(reason, "_pending") {
					detail.DetailState = "pending"
				}
			}
		}
	}
	return detail, nil
}

func missingTrace(detail *requesttrace.TraceDetail, now time.Time) *requesttrace.TraceDetail {
	age := now.Sub(time.UnixMilli(detail.Summary.StartedAtMS))
	switch {
	case age < 30*time.Second:
		detail.DetailState = "pending"
	case age > 48*time.Hour:
		detail.DetailState = "expired"
	default:
		detail.DetailState = "partial"
	}
	detail.MissingReasons = []string{"trace_not_found"}
	return detail
}
func traceIDHex(value string, bytes int) string {
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == bytes {
		return strings.ToLower(value)
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == bytes {
		return hex.EncodeToString(decoded)
	}
	return ""
}
func appendReason(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

type locatedSpan struct {
	service string
	span    otlpSpan
}
type traceBudget struct{ bytes, spans int }

var errQuery = errors.New("trace query failed")
var errTraceMissing = errors.New("trace missing")

func (r *Repository) fetchTrace(ctx context.Context, id string, budget *traceBudget) ([]locatedSpan, bool, error) {
	u := r.data.traceEndpoint + "/api/v3/traces/" + id + "?raw_traces=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false, errQuery
	}
	resp, err := r.data.traceHTTP.Do(req) // #nosec G704 -- trusted owner config, redirects disabled, trace ID validated on ingestion.
	if err != nil {
		return nil, false, errQuery
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, false, errTraceMissing
	}
	if resp.StatusCode != 200 {
		return nil, false, errQuery
	}
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, int64(budget.bytes)+1))
	if err != nil {
		return nil, false, errQuery
	}
	if len(encoded) > budget.bytes {
		return nil, true, nil
	}
	budget.bytes -= len(encoded)
	decoder := jsonx.NewDecoder(bytes.NewReader(encoded))
	var spans []locatedSpan
	truncated := false
	for {
		var envelope traceEnvelope
		err = decoder.Decode(&envelope)
		if err == io.EOF {
			break
		}
		if err != nil || len(envelope.Error) > 0 {
			return nil, false, errQuery
		}
		for _, resource := range envelope.Result.ResourceSpans {
			service := "unknown"
			for _, a := range resource.Resource.Attributes {
				if a.Key == "service.name" {
					service = a.Value.String
				}
			}
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if budget.spans == 0 {
						truncated = true
						continue
					}
					spans = append(spans, locatedSpan{service, span})
					budget.spans--
				}
			}
		}
	}
	return spans, truncated, nil
}
