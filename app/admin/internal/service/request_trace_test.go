package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/requesttrace"
)

type settlementStatusBillingClient struct {
	billingv1.BillingServiceClient
	reply *billingv1.GetSettlementStatusResponse
	err   error
	got   string
}

func (c *settlementStatusBillingClient) GetSettlementStatus(_ context.Context, req *billingv1.GetSettlementStatusRequest, _ ...grpc.CallOption) (*billingv1.GetSettlementStatusResponse, error) {
	c.got = req.ReservationId
	return c.reply, c.err
}

func traceDetailWithAttempts(attempts ...requesttrace.TimingAttempt) *requesttrace.TraceDetail {
	return &requesttrace.TraceDetail{Summary: &requesttrace.TimingSummary{Attempts: attempts}}
}

func TestAttachSettlementCurrentUsesLastReservedAttempt(t *testing.T) {
	pending := "res-old"
	current := "res-new"
	client := &settlementStatusBillingClient{reply: &billingv1.GetSettlementStatusResponse{Status: "committed", ObservedAtMs: 123, Source: "reservation"}}
	s := &AdminService{billingClient: client}
	view := traceDetailWithAttempts(
		requesttrace.TimingAttempt{Number: 1, ReservationID: &pending},
		requesttrace.TimingAttempt{Number: 2, ReservationID: &current},
	)
	s.attachSettlementCurrent(context.Background(), view)
	require.Equal(t, "res-new", client.got)
	require.NotNil(t, view.SettlementCurrent)
	require.Equal(t, "committed", view.SettlementCurrent.Status)
	require.EqualValues(t, 123, view.SettlementCurrent.ObservedAtMS)
	require.Equal(t, "reservation", view.SettlementCurrent.Source)
}

func TestAttachSettlementCurrentNeverBlocksTheDetail(t *testing.T) {
	reserved := "res-1"
	for name, tc := range map[string]struct {
		client *settlementStatusBillingClient
		view   *requesttrace.TraceDetail
	}{
		"no reservation":              {client: &settlementStatusBillingClient{}, view: traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1})},
		"permission denied":           {client: &settlementStatusBillingClient{err: status.Error(codes.PermissionDenied, "denied")}, view: traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1, ReservationID: &reserved})},
		"billing unavailable":         {client: &settlementStatusBillingClient{err: status.Error(codes.Unavailable, "down")}, view: traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1, ReservationID: &reserved})},
		"reservation unknown":         {client: &settlementStatusBillingClient{err: status.Error(codes.NotFound, "missing")}, view: traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1, ReservationID: &reserved})},
		"unexpected transport error":  {client: &settlementStatusBillingClient{err: errors.New("boom")}, view: traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1, ReservationID: &reserved})},
		"empty status is not applied": {client: &settlementStatusBillingClient{reply: &billingv1.GetSettlementStatusResponse{}}, view: traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1, ReservationID: &reserved})},
	} {
		t.Run(name, func(t *testing.T) {
			s := &AdminService{billingClient: tc.client}
			s.attachSettlementCurrent(context.Background(), tc.view)
			require.Nil(t, tc.view.SettlementCurrent)
		})
	}

	t.Run("nil billing client", func(t *testing.T) {
		s := &AdminService{}
		view := traceDetailWithAttempts(requesttrace.TimingAttempt{Number: 1, ReservationID: &reserved})
		s.attachSettlementCurrent(context.Background(), view)
		require.Nil(t, view.SettlementCurrent)
	})
}
