package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/admin/internal/service"
)

type traceReferenceClient struct{ billingv1.BillingServiceClient }

func (*traceReferenceClient) ResolveLedgerRequest(context.Context, *billingv1.ResolveLedgerRequestRequest, ...grpc.CallOption) (*billingv1.ResolveLedgerRequestResponse, error) {
	return &billingv1.ResolveLedgerRequestResponse{Applicable: true, UserId: 42, ReservationId: "reservation-42"}, nil
}

type traceListClient struct {
	logv1.LogServiceClient
	got *logv1.ListRequestTracesRequest
}

func (c *traceListClient) ListRequestTraces(_ context.Context, req *logv1.ListRequestTracesRequest, _ ...grpc.CallOption) (*logv1.ListRequestTracesResponse, error) {
	c.got = req
	return &logv1.ListRequestTracesResponse{}, nil
}

func TestLedgerTraceRoutePreservesPaginationSnapshot(t *testing.T) {
	client := &traceListClient{}
	svc := service.NewAdminService(&traceReferenceClient{}, nil, nil, nil)
	svc.SetLogClient(client)
	r := httptest.NewRequest(http.MethodGet, "/api/log/7/trace?page_token=owner-cursor", nil)
	w := httptest.NewRecorder()
	handleRequestTrace(w, r, svc)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, client.got)
	require.Equal(t, "owner-cursor", client.got.PageToken)
	// The owner restores the original time bounds from the cursor. Generating
	// a new end time on each page invalidates its filter digest.
	require.Zero(t, client.got.EndTimeMs)
	require.Equal(t, "reservation-42", client.got.ReservationId)
	require.EqualValues(t, 42, *client.got.UserId)
}
