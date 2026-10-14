package server

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/biz"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeV2PreservesZeroAndUnprovenStates(t *testing.T) {
	zero := evidence.Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), "openai_chat")
	u := nativeV2Envelope(&evidence.Record{Observation: zero, Complete: true, HTTPStatus: 200})
	require.Equal(t, "verified", string(u.ParseStatus))
	require.NotNil(t, u.Canonical)
	require.Zero(t, u.BillableTotal())
	for _, status := range []string{"missing", "unsupported", "invalid"} {
		u = nativeV2Envelope(&evidence.Record{Observation: evidence.Observation{Status: status, Reason: "fixture"}, Complete: true})
		require.Equal(t, status, string(u.ParseStatus))
		require.Nil(t, u.Canonical)
	}
	u = nativeV2Envelope(&evidence.Record{Observation: zero, Complete: false})
	require.Equal(t, "missing", string(u.ParseStatus))
	require.Nil(t, u.Canonical)
}

func TestDurableResponsesNeverRetriesByChangingProtocol(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"endpoint unsupported"}`)
			}))
			defer upstream.Close()
			factory := provider.NewProviderFactory(time.Second)
			_ = NewHTTPServer(nil, nil, nil, factory, nil)
			f := newRelayAdaptorForwarder(factory, nil, nil, nil, nil)
			var records []evidence.Record
			ctx := evidence.WithDurableRecorder(context.Background(), nil, func(r evidence.Record) error { records = append(records, r); return nil })
			plan := &biz.RelayPlan{Channel: &biz.Channel{ID: 1, Type: provider.ChannelTypeOpenAI, BaseURL: upstream.URL + "/v1", Key: "fixture"}, ResolvedModel: "m"}
			req := biz.ExecutorRequest{Endpoint: "responses", Model: "m", Body: []byte(`{"model":"m","input":"ping"}`), Stream: stream}
			var err error
			if stream {
				_, err = f.(relayAdaptorForwarder).ForwardStream(ctx, plan, req)
			} else {
				_, err = f.Forward(ctx, plan, req)
			}
			require.Error(t, err)
			require.Equal(t, 1, calls, "each supplier send needs a separate durable intent")
			require.NotEmpty(t, records)
			require.Equal(t, "upstream_rejected", records[len(records)-1].Execution)
		})
	}
}

func TestDurableIntentCannotAuthorizeASecondSend(t *testing.T) {
	s := &HTTPServer{}
	s.executionAttempts.Store("attempt", &durableAttempt{})
	ctx := s.executionContext(context.Background(), "attempt")
	require.NoError(t, evidence.CheckDurable(ctx))
	err := evidence.CheckDurable(ctx)
	require.Error(t, err)
	require.True(t, biz.IsPostForwardError(err))
}

type checkpointFailOutbox struct {
	biz.ExecutionOutboxRepo
	events []execution.Event
}

func (r *checkpointFailOutbox) Put(_ context.Context, e execution.Event) error {
	if e.Category == "checkpoint" {
		return fmt.Errorf("checkpoint capacity exhausted")
	}
	r.events = append(r.events, e)
	return nil
}
func (*checkpointFailOutbox) Send(_ context.Context, e execution.Event) (execution.Receipt, error) {
	_, digest, err := e.Encode()
	return execution.Receipt{Received: true, Digest: digest, SettlementAccepted: true}, err
}
func (*checkpointFailOutbox) Acknowledge(context.Context, string, string) error { return nil }

func TestDurableTerminalCanReleaseAfterCheckpointFailure(t *testing.T) {
	now := time.Now()
	i := execution.Freeze(execution.Intent{ReservationID: "res", RequestID: "attempt", RootRequestID: "attempt", AttemptNumber: 1, Purpose: "user", SourceKind: "channel", SourceID: 1, Model: "m", Policy: execution.Policy, LeaseUntil: now.Add(time.Minute).Unix(), EvidenceDeadline: now.Add(time.Hour).Unix(), CostBasis: "unknown"})
	repo := &checkpointFailOutbox{}
	s := &HTTPServer{}
	s.executionAttempts.Store("attempt", &durableAttempt{intent: i, delivery: biz.NewExecutionDelivery(repo)})
	a, _ := s.executionAttempts.Load("attempt")
	s.executionAttempts.Store("res", a)
	ctx := s.executionContext(context.Background(), "attempt")
	err := evidence.RecordDurable(ctx, evidence.Record{Execution: "checkpoint"})
	require.Error(t, err)
	require.True(t, biz.IsPostForwardError(err), "persistence failure after sending must not replay upstream")
	require.NoError(t, evidence.RecordDurable(ctx, evidence.Record{Execution: "incomplete", Observation: evidence.Observation{Protocol: "openai_chat", Status: "missing", Reason: "usage_absent"}}))
	_, err = s.durableCommit(ctx, &billingv1.CommitQuotaRequest{ReservationId: "res", Success: false})
	require.NoError(t, err, "persisted incomplete terminal must allow the frozen release policy")
	require.Equal(t, "final", repo.events[len(repo.events)-1].Category)
}
