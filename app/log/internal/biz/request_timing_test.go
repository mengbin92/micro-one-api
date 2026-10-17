package biz

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/requesttrace"
)

func testTiming() *requesttrace.TimingSummary {
	return &requesttrace.TimingSummary{Version: 1, ObservationID: uuid.NewString(), ObservationKind: "http",
		Protocol: "http", Endpoint: "/v1/chat/completions", ExecutionPath: "legacy", UserID: 7,
		RootRequestID: "root", StartedAtMS: 1000, Outcome: "failed", CollectionState: "not_recorded"}
}

func TestIngestTimingIsDurableNormalizedAndIsolated(t *testing.T) {
	repo := &mockLogRepo{entries: map[int64]*LogEntry{}}
	uc := NewLogUsecase(repo)
	// An installed batch writer must not turn summary acknowledgement into an
	// enqueue-only acknowledgement. No worker is needed for this assertion.
	writer := NewBatchLogWriter(repo, 10, time.Hour)
	uc.SetBatchWriter(writer)
	t.Cleanup(writer.Stop)
	s := testTiming()
	e := &LogEntry{Source: requesttrace.TimingSource, RequestTiming: s, UserID: 7, RootRequestID: "root",
		DedupeKey: "consume:7:other", Message: "must not persist", ModelName: "model", Quota: 99, PromptTokens: 50}
	require.NoError(t, uc.IngestLog(context.Background(), e))
	require.NotZero(t, e.ID, "timing summaries must use durable deduplicated ingestion")
	require.Equal(t, requesttrace.TimingDedupeKey(s.ObservationID), e.DedupeKey)
	require.Empty(t, e.Message)
	require.Empty(t, e.ModelName)
	require.Zero(t, e.Quota)
	require.Zero(t, e.PromptTokens)
	s.Outcome = "success"
	require.Equal(t, "failed", repo.entries[e.ID].RequestTiming.Outcome)
}

func TestIngestTimingRejectsOwnerAndSourceMismatch(t *testing.T) {
	for _, mutate := range []func(*LogEntry){
		func(e *LogEntry) { e.UserID++ },
		func(e *LogEntry) { e.RootRequestID = "different" },
		func(e *LogEntry) { e.Source = "relay-gateway" },
		func(e *LogEntry) { e.RequestTiming = nil },
	} {
		repo := &mockLogRepo{entries: map[int64]*LogEntry{}}
		s := testTiming()
		e := &LogEntry{Source: requesttrace.TimingSource, RequestTiming: s, UserID: s.UserID, RootRequestID: s.RootRequestID}
		mutate(e)
		require.ErrorIs(t, NewLogUsecase(repo).IngestLog(context.Background(), e), ErrInvalidRequestTiming)
		require.Empty(t, repo.entries)
	}
}
