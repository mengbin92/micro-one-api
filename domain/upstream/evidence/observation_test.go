package evidence

import (
	"context"
	"testing"
	"time"
)

func observationSend() SendRef {
	return SendRef{ObservationID: "obs-1", SendID: "obs-1/1/send-1", RequestID: "req-1", RootRequestID: "root-1",
		AttemptNumber: 1, UserID: 7, Purpose: PurposeUser, SourceKind: "channel", SourceID: 3, Model: "exact-model",
		Protocol: "openai_chat", Endpoint: "/v1/chat/completions", APIVersion: "unknown"}
}

func verifiedRecord() Record {
	b := [5]int64{10, 2, 0, 0, 3}
	return Record{Observation: Observation{Protocol: "openai_chat", ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "verified", Buckets: &b, UsageDigest: "digest"}, HTTPStatus: 200, Complete: true, Execution: "response_received"}
}

func TestObservationEventIdentityAndDigestAreStable(t *testing.T) {
	record := verifiedRecord()
	event := NewSendEvent(observationSend(), "terminal", 1, &record, CaptureComplete, "", time.Unix(1700000000, 0))
	if event.ID != "obs-1/1/send-1/terminal/1" {
		t.Fatalf("unexpected event id %q", event.ID)
	}
	first, firstDigest, err := event.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	second, secondDigest, err := event.Encode()
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if string(first) != string(second) || firstDigest != secondDigest {
		t.Fatal("observation encoding is not deterministic; replay would be rejected")
	}

	changed := event
	changed.Record = &evidenceRecordCopy
	_, changedDigest, err := changed.Encode()
	if err != nil {
		t.Fatalf("encode changed: %v", err)
	}
	if changedDigest == firstDigest {
		t.Fatal("different evidence produced the same digest")
	}
}

var evidenceRecordCopy = Record{
	Observation: Observation{Protocol: "openai_chat", Status: "missing", Reason: "usage_absent"},
	HTTPStatus:  200, Complete: true, Execution: "response_received",
}

func TestObservationEventRejectsUnsafeOrUnboundedValues(t *testing.T) {
	record := verifiedRecord()
	for name, mutate := range map[string]func(*SendEvent){
		"query in endpoint": func(e *SendEvent) { e.Send.Endpoint = "/v1/chat/completions?key=secret" },
		"wrong key":         func(e *SendEvent) { e.ID = "other" },
		"bad purpose":       func(e *SendEvent) { e.Send.Purpose = "customer" },
		"unknown source":    func(e *SendEvent) { e.Send.SourceKind = "vendor"; e.Send.SourceID = 0 },
		"terminal no record": func(e *SendEvent) {
			e.Record = nil
		},
		"started with capture": func(e *SendEvent) {
			e.Category, e.Sequence, e.Record, e.Capture, e.ID = "started", 0, nil, CaptureComplete, e.Send.SendID+"/started/0"
		},
		"not_sent with record": func(e *SendEvent) {
			e.Category, e.Sequence, e.Capture, e.Reason = "not_sent", 0, CaptureNotSent, NotSentAdmissionRejected
			e.ID = e.Send.SendID + "/not_sent/0"
		},
	} {
		t.Run(name, func(t *testing.T) {
			event := NewSendEvent(observationSend(), "terminal", 1, &record, CaptureComplete, "", time.Now())
			mutate(&event)
			if err := event.Validate(); err == nil {
				t.Fatalf("expected validation failure for %s", name)
			}
		})
	}
	if err := NewSendEvent(observationSend(), "started", 0, nil, "", "", time.Now()).Validate(); err != nil {
		t.Fatalf("started must be valid without evidence: %v", err)
	}
	notSentRef := observationSend()
	notSentRef.SendID = ""
	notSent := NewSendEvent(notSentRef, "not_sent", 0, nil, CaptureNotSent, NotSentAdmissionRejected, time.Now())
	if err := notSent.Validate(); err != nil {
		t.Fatalf("not_sent must be valid: %v", err)
	}
	if notSent.ID != notSent.Key() || notSent.ID == "obs-1/not_sent/0" {
		t.Fatalf("not_sent must key on the request, not a fabricated send: %s", notSent.ID)
	}
	if withSend := NewSendEvent(observationSend(), "not_sent", 0, nil, CaptureNotSent, NotSentAdmissionRejected, time.Now()); withSend.Validate() == nil {
		t.Fatal("not_sent carrying a send id must be rejected")
	}
}

func TestClassifyCaptureSeparatesTransportOutcomes(t *testing.T) {
	complete := verifiedRecord()
	if got := ClassifyCapture(complete, false); got != CaptureComplete {
		t.Fatalf("complete: %s", got)
	}
	if got := ClassifyCapture(complete, true); got != CaptureClientCancel {
		t.Fatalf("downstream cancel: %s", got)
	}
	transport := Record{Observation: Observation{Status: "missing", Reason: "transport_error"}, Execution: "execution_unknown"}
	if got := ClassifyCapture(transport, false); got != CaptureTransportError {
		t.Fatalf("transport: %s", got)
	}
	rejected := Record{Observation: Observation{Status: "missing"}, Execution: "upstream_rejected", HTTPStatus: 429}
	if got := ClassifyCapture(rejected, false); got != CaptureUpstreamRejected {
		t.Fatalf("rejected: %s", got)
	}
	limited := Record{Observation: Observation{Status: "unobserved", Reason: "frame_limit"}, Execution: "incomplete"}
	if got := ClassifyCapture(limited, false); got != CaptureLimited {
		t.Fatalf("limited: %s", got)
	}
	truncated := Record{Observation: Observation{Status: "verified"}, Execution: "incomplete", HTTPStatus: 200}
	if got := ClassifyCapture(truncated, false); got != CaptureTruncated {
		t.Fatalf("truncated: %s", got)
	}
	failed := Record{Observation: Observation{Status: "verified", Terminal: "failed"}, Complete: true, HTTPStatus: 200}
	if got := ClassifyCapture(failed, false); got != CaptureUpstreamRejected {
		t.Fatalf("terminal failure: %s", got)
	}
}

func TestSinkFromContextIsOptional(t *testing.T) {
	if ObservationSinkFrom(context.Background()) != nil {
		t.Fatal("background context must not carry an observation sink")
	}
	sink := &recordingSink{}
	if ObservationSinkFrom(WithObservationSink(context.Background(), sink)) != sink {
		t.Fatal("sink was not attached")
	}
}

type recordingSink struct {
	started  int
	terminal []Record
}

func (s *recordingSink) Started(string, string, string) { s.started++ }
func (s *recordingSink) Checkpoint(Record)              {}
func (s *recordingSink) Terminal(record Record)         { s.terminal = append(s.terminal, record) }
func (s *recordingSink) TransportFail(string, string)   {}
func (s *recordingSink) Redirect(string, string, string, int) {
	s.started++
}
func (s *recordingSink) NotSent(string, string) {}
