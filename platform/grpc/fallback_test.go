package grpc

import (
	"context"
	"errors"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

// --- Stubs implementing the lookup/queue interfaces ---

type stubAuthLookup struct {
	snap *identityv1.GetAuthSnapshotReply
	err  error
}

func (s *stubAuthLookup) Lookup(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	return s.snap, s.err
}

type stubChannelLookup struct {
	ch  *commonv1.ChannelInfo
	err error
}

func (s *stubChannelLookup) Lookup(ctx context.Context, group, model string) (*commonv1.ChannelInfo, error) {
	return s.ch, s.err
}

type stubBillingQueue struct {
	resp *billingv1.ReserveQuotaResponse
	err  error
}

func (s *stubBillingQueue) Enqueue(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	return s.resp, s.err
}

// --- Tests ---

func TestAuthCacheFallback_NoLookupRejects(t *testing.T) {
	f := NewAuthCacheFallback()
	_, err := f.ExecuteFallback(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error when no lookup configured, got nil")
	}
}

func TestAuthCacheFallback_WithLookupReturnsSnapshot(t *testing.T) {
	want := &identityv1.GetAuthSnapshotReply{UserId: 42}
	f := NewAuthCacheFallback().WithLookup(&stubAuthLookup{snap: want})
	got, err := f.ExecuteFallback(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ExecuteFallback: %v", err)
	}
	if got.GetUserId() != 42 {
		t.Fatalf("got user %d, want 42", got.GetUserId())
	}
}

func TestAuthCacheFallback_LookupErrorPropagates(t *testing.T) {
	f := NewAuthCacheFallback().WithLookup(&stubAuthLookup{err: errors.New("boom")})
	_, err := f.ExecuteFallback(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected propagated error, got nil")
	}
}

func TestAuthCacheFallback_NilSnapshotRejects(t *testing.T) {
	f := NewAuthCacheFallback().WithLookup(&stubAuthLookup{snap: nil})
	_, err := f.ExecuteFallback(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error for nil snapshot, got nil")
	}
}

func TestChannelCacheFallback_NoLookupRejects(t *testing.T) {
	f := NewChannelCacheFallback()
	_, err := f.ExecuteFallback(context.Background(), "g", "m")
	if err == nil {
		t.Fatal("expected error when no lookup configured, got nil")
	}
}

func TestChannelCacheFallback_WithLookupReturnsChannel(t *testing.T) {
	want := &commonv1.ChannelInfo{Id: 7}
	f := NewChannelCacheFallback().WithLookup(&stubChannelLookup{ch: want})
	got, err := f.ExecuteFallback(context.Background(), "g", "m")
	if err != nil {
		t.Fatalf("ExecuteFallback: %v", err)
	}
	if got.GetId() != 7 {
		t.Fatalf("got id %d, want 7", got.GetId())
	}
}

func TestChannelCacheFallback_LookupErrorPropagates(t *testing.T) {
	f := NewChannelCacheFallback().WithLookup(&stubChannelLookup{err: errors.New("boom")})
	_, err := f.ExecuteFallback(context.Background(), "g", "m")
	if err == nil {
		t.Fatal("expected propagated error, got nil")
	}
}

func TestAsyncBillingFallback_NoQueueRejects(t *testing.T) {
	f := NewAsyncBillingFallback()
	_, err := f.ExecuteFallback(context.Background(), &billingv1.ReserveQuotaRequest{RequestId: "r"})
	if err == nil {
		t.Fatal("expected error when no queue configured, got nil (was fake success)")
	}
}

func TestAsyncBillingFallback_WithQueueReturnsResponse(t *testing.T) {
	want := &billingv1.ReserveQuotaResponse{Success: true, ReservationId: "async-r"}
	f := NewAsyncBillingFallback().WithQueue(&stubBillingQueue{resp: want})
	got, err := f.ExecuteFallback(context.Background(), &billingv1.ReserveQuotaRequest{RequestId: "r"})
	if err != nil {
		t.Fatalf("ExecuteFallback: %v", err)
	}
	if !got.GetSuccess() || got.GetReservationId() != "async-r" {
		t.Fatalf("got %+v, want success+async-r", got)
	}
}

func TestAsyncBillingFallback_QueueErrorPropagates(t *testing.T) {
	f := NewAsyncBillingFallback().WithQueue(&stubBillingQueue{err: errors.New("queue full")})
	_, err := f.ExecuteFallback(context.Background(), &billingv1.ReserveQuotaRequest{RequestId: "r"})
	if err == nil {
		t.Fatal("expected propagated error, got nil")
	}
}

func TestAsyncBillingFallback_NilResponseRejects(t *testing.T) {
	f := NewAsyncBillingFallback().WithQueue(&stubBillingQueue{resp: nil})
	_, err := f.ExecuteFallback(context.Background(), &billingv1.ReserveQuotaRequest{RequestId: "r"})
	if err == nil {
		t.Fatal("expected error for nil response, got nil")
	}
}

func TestRejectFallback(t *testing.T) {
	r := NewRejectFallback("identity")
	if err := r.ExecuteFallback(context.Background()); err == nil {
		t.Fatal("expected error from RejectFallback")
	}
}

func TestNoOpFallback(t *testing.T) {
	n := NewNoOpFallback()
	if err := n.ExecuteFallback(context.Background()); err != nil {
		t.Fatalf("NoOpFallback returned error: %v", err)
	}
}

func TestDegradationLevel_String(t *testing.T) {
	cases := map[DegradationLevel]string{
		DegradationNone:      "none",
		DegradationCached:    "cached",
		DegradationAsync:     "async",
		DegradationMinimal:   "minimal",
		DegradationLevel(99): "unknown",
	}
	for lvl, want := range cases {
		if got := lvl.String(); got != want {
			t.Errorf("DegradationLevel(%d).String() = %q, want %q", lvl, got, want)
		}
	}
}
