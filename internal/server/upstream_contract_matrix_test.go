package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
)

type contractMatrixTransport func(*http.Request) (*http.Response, error)

func (f contractMatrixTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpstreamContractEvidenceAcrossTransports(t *testing.T) {
	t.Setenv("RELAY_UPSTREAM_EXECUTION_TIME_V1", "true")
	doc := contract.Document{Scope: contract.Scope{Kind: "channel", ID: 1, Model: "exact", Protocol: "responses"}, Provider: "fixture", AdapterBuild: contract.BuildID, ParserRevision: evidence.ParserRevision, UsageRevision: evidence.ContractRevision, Inbound: []string{"responses"}, FixtureEvidence: "independent-transport-matrix", BasisRevision: "fixture-v1", Sets: []contract.RuleSet{{Level: "model", Rules: []contract.Rule{{ID: "default", Kind: "default", Path: "temperature", Type: "number", Value: jsonx.RawMessage(`0`), Evidence: "fixture"}}}}}
	snapshot, err := contract.Compile(doc)
	require.NoError(t, err)
	snapshot.BindingRevision, snapshot.IncidentRevision = 4, 2
	for _, transport := range []string{"http", "sse", "websocket"} {
		for _, scenario := range []string{"verified", "missing_usage", "truncated", "quarantined"} {
			t.Run(transport+"/"+scenario, func(t *testing.T) {
				selection := contract.Selection{Snapshots: []contract.Snapshot{snapshot}, Inbound: "responses"}
				if scenario == "quarantined" {
					selection.Blocked = []string{"responses"}
				}
				var records []evidence.Record
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				ctx = contract.WithSelection(ctx, selection)
				ctx = evidence.WithRecorder(ctx, func(r evidence.Record) { records = append(records, r) })
				payload := `{"id":"r1","status":"completed","usage":{"input_tokens":2,"output_tokens":1}}`
				if scenario == "missing_usage" {
					payload = `{"id":"r1","status":"completed"}`
				}
				if scenario == "truncated" {
					payload = `{"id":"r1","status":"in_progress","usage":{"input_tokens":2,"output_tokens":1}}`
				}
				requestBody := []byte(`{"type":"response.create","model":"exact","unknown":null}`)
				var forwarded []byte
				var sendErr error
				if transport == "websocket" {
					client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
					event := "response.completed"
					if scenario == "truncated" {
						event = "response.in_progress"
					}
					upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"` + event + `","response":` + payload + `}`)}
					close(upstream.in)
					_, exit := relayOpenAIWSFrames(ctx, client, upstream, requestBody, openAIWSRelayOptions{contractSelection: func() contract.Selection { return selection }, durableRecord: func(evidence.Record) error { return nil }})
					if scenario == "quarantined" {
						require.NotNil(t, exit)
						sendErr = exit.err
					}
					select {
					case frame := <-upstream.out:
						forwarded = frame.payload
					default:
					}
				} else {
					client := &http.Client{Transport: contractMatrixTransport(func(req *http.Request) (*http.Response, error) {
						forwarded, _ = io.ReadAll(req.Body)
						body, contentType := payload, "application/json"
						if transport == "sse" {
							event := "response.completed"
							if scenario == "truncated" {
								event = "response.in_progress"
							}
							body, contentType = "data: {\"type\":\""+event+"\",\"response\":"+payload+"}\n\n", "text/event-stream"
						}
						var reader io.Reader = strings.NewReader(body)
						if scenario == "truncated" {
							reader = io.MultiReader(reader, iotest.ErrReader(io.ErrUnexpectedEOF))
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(reader)}, nil
					})}
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/proxy/v1/responses", strings.NewReader(string(requestBody)))
					require.NoError(t, err)
					response, err := evidence.Do(client, req, "responses")
					sendErr = err
					if err == nil {
						_, readErr := io.Copy(io.Discard, response.Body)
						if scenario == "truncated" {
							require.ErrorIs(t, readErr, io.ErrUnexpectedEOF)
						} else {
							require.NoError(t, readErr)
						}
						require.NoError(t, response.Body.Close())
					}
				}
				if scenario == "quarantined" {
					require.ErrorIs(t, sendErr, contract.ErrQuarantined)
					require.Empty(t, forwarded, "quarantine must reject before supplier I/O")
					return
				}
				require.Contains(t, string(forwarded), `"temperature":0`)
				require.Contains(t, string(forwarded), `"unknown":null`)
				require.Len(t, records, 1)
				record := records[0]
				require.NotNil(t, record.ParameterContract)
				require.Equal(t, snapshot.Revision, record.ParameterContract.Revision)
				require.EqualValues(t, 4, record.ParameterContract.BindingRevision)
				require.EqualValues(t, 2, record.ParameterContract.IncidentRevision)
				require.Equal(t, evidence.ParserRevision, record.Observation.ParserRevision)
				require.Equal(t, evidence.ContractRevision, record.Observation.ContractRevision)
				require.Positive(t, record.StartedAtUnixNano)
				if scenario == "missing_usage" {
					require.NotEqual(t, "verified", record.Observation.Status)
				}
				if scenario == "verified" {
					require.Equal(t, "verified", record.Observation.Status)
					require.True(t, record.Complete)
				}
				if scenario == "truncated" {
					require.False(t, record.Complete)
					require.Equal(t, "incomplete", record.Execution)
				}
			})
		}
	}
}

func TestUpstreamContractTypeMismatchRejectsBeforeTransport(t *testing.T) {
	for _, kind := range []string{"rename", "default"} {
		rule := contract.Rule{ID: "typed-option", Kind: kind, Path: "option", Type: "number", Evidence: "independent-type-fixture"}
		if kind == "rename" {
			rule.Target = "new_option"
		} else {
			rule.Value = jsonx.RawMessage(`0`)
		}
		doc := contract.Document{Scope: contract.Scope{Kind: "channel", ID: 1, Model: "exact", Protocol: "responses"}, Provider: "fixture", AdapterBuild: contract.BuildID, ParserRevision: evidence.ParserRevision, UsageRevision: evidence.ContractRevision, Inbound: []string{"responses"}, FixtureEvidence: "independent-transport-type-matrix", BasisRevision: "fixture-types-v1", Sets: []contract.RuleSet{{Level: "model", Rules: []contract.Rule{rule}}}}
		snapshot, err := contract.Compile(doc)
		require.NoError(t, err)
		selection := contract.Selection{Snapshots: []contract.Snapshot{snapshot}, Inbound: "responses"}
		for _, transport := range []string{"http", "sse", "websocket"} {
			for _, value := range []string{`"0"`, `null`, `false`, `{}`, `[]`} {
				paths := []string{"option"}
				if kind == "rename" {
					paths = append(paths, "new_option")
				}
				for _, path := range paths {
					t.Run(kind+"/"+transport+"/"+path+"/"+value, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						ctx = contract.WithSelection(ctx, selection)
						body := []byte(`{"type":"response.create","model":"exact","` + path + `":` + value + `}`)
						if transport == "sse" {
							body = []byte(`{"model":"exact","stream":true,"` + path + `":` + value + `}`)
						}
						var sendErr error
						if transport == "websocket" {
							client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
							_, exit := relayOpenAIWSFrames(ctx, client, upstream, body, openAIWSRelayOptions{contractSelection: func() contract.Selection { return selection }})
							require.NotNil(t, exit)
							sendErr = exit.err
							require.Empty(t, upstream.out, "a rejected request must not write an upstream frame")
						} else {
							calls := 0
							client := &http.Client{Transport: contractMatrixTransport(func(*http.Request) (*http.Response, error) {
								calls++
								return nil, io.ErrUnexpectedEOF
							})}
							req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/v1/responses", strings.NewReader(string(body)))
							require.NoError(t, err)
							if transport == "sse" {
								req.Header.Set("Accept", "text/event-stream")
							}
							_, sendErr = evidence.Do(client, req, "responses")
							require.Zero(t, calls, "a rejected request must not call the upstream transport")
						}
						var rejected *contract.Rejection
						require.ErrorAs(t, sendErr, &rejected)
						require.True(t, rejected.BeforeUpstream())
						require.ErrorContains(t, sendErr, path)
					})
				}
			}
		}
	}
}
