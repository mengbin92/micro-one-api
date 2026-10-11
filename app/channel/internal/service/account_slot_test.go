package service

import (
	"context"
	"strings"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
)

func TestChannelService_RecordSubscriptionAccountSlotLeases(t *testing.T) {
	ctx := context.Background()
	uc := biz.NewChannelUsecase(&channelServiceRepo{}, nil)
	svc := NewChannelService(uc)
	for _, req := range []*channelv1.RecordSubscriptionAccountSlotRequest{
		{},
		{AccountId: 7},
		{AccountId: 7, SlotId: " "},
		{AccountId: 7, SlotId: strings.Repeat("x", 129)},
	} {
		reply, err := svc.RecordSubscriptionAccountSlot(ctx, req)
		if err != nil || reply.GetSuccess() {
			t.Fatalf("invalid request result = %v, %v", reply, err)
		}
	}
	for _, req := range []*channelv1.RecordSubscriptionAccountSlotRequest{
		{AccountId: 7, SlotId: "first", Acquired: true},
		{AccountId: 7, SlotId: "first", Acquired: true},
		{AccountId: 7, SlotId: "other", Acquired: true},
		{AccountId: 7, SlotId: "first", Acquired: false},
		{AccountId: 7, SlotId: "first", Acquired: false},
	} {
		reply, err := svc.RecordSubscriptionAccountSlot(ctx, req)
		if err != nil || !reply.GetSuccess() {
			t.Fatalf("valid lease result = %v, %v", reply, err)
		}
	}
	if got := uc.AccountSelectorStats()[7]; got.Inflight != 1 || got.ErrorRate != 0 {
		t.Fatalf("duplicate lease reports changed other load or health: %+v", got)
	}
	reply, err := svc.RecordSubscriptionAccountSlot(ctx, &channelv1.RecordSubscriptionAccountSlotRequest{AccountId: 7, SlotId: "first", Acquired: true})
	if err != nil || reply.GetSuccess() {
		t.Fatalf("released ID was acquired again: %v, %v", reply, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.RecordSubscriptionAccountSlot(cancelled, &channelv1.RecordSubscriptionAccountSlotRequest{AccountId: 7, SlotId: "cancelled", Acquired: true}); err == nil {
		t.Fatal("cancelled acquire must not mutate load telemetry")
	}
	if got := uc.AccountSelectorStats()[7].Inflight; got != 1 {
		t.Fatalf("cancelled acquire changed other load: inflight=%d", got)
	}
}
