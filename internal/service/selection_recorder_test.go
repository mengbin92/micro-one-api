package service

import (
	"context"
	"testing"

	logv1 "micro-one-api/api/log/v1"
	relaybiz "micro-one-api/internal/biz"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type selectionLogClient struct {
	logv1.LogServiceClient
	req *logv1.IngestLogRequest
}

func (c *selectionLogClient) IngestLog(_ context.Context, req *logv1.IngestLogRequest, _ ...grpc.CallOption) (*logv1.IngestLogResponse, error) {
	c.req = req
	return &logv1.IngestLogResponse{Id: 1}, nil
}

func TestSelectionAuditRecorderPersistsScopedOutcome(t *testing.T) {
	client := &selectionLogClient{}
	recorder := NewSelectionAuditRecorder(nil, client)
	recorder.RecordSelection(context.Background(), relaybiz.SelectionEvent{
		RequestID: "root-1", RootRequestID: "root-1", UserID: 42,
		Model: "gpt-4o", FinalKind: "channel", FinalSourceID: 7,
		CandidateKinds: []string{"channel", "subscription"}, Result: "success",
		Fallback: true, FallbackReason: "upstream_5xx", ElapsedMS: 25,
	})

	require.NotNil(t, client.req)
	require.Equal(t, "routing-selection", client.req.Source)
	require.Equal(t, int64(42), client.req.UserId)
	require.Equal(t, "root-1", client.req.RootRequestId)
	require.Equal(t, int64(7), client.req.ChannelId)
	require.Equal(t, "routing-selection:42:root-1:outcome", client.req.DedupeKey)
	require.JSONEq(t, `{"candidate_kinds":["channel","subscription"],"sticky_hit":false,"priority_tier":0,"fallback":true,"fallback_reason":"upstream_5xx","result":"success","provider_family":"","planned":false}`, client.req.Message)
}
