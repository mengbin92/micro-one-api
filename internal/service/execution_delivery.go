package service

import (
	"context"
	"errors"
	"fmt"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/billingdto"
	"sync"
	"time"
)

type grpcDurableAttempt struct {
	mu       sync.Mutex
	intent   execution.Intent
	last     *evidence.Record
	sequence int64
	err      error
}

func (s *RelayGrpcService) SetExecutionDelivery(d *relaybiz.ExecutionDelivery) {
	s.executionDelivery = d
}
func (s *RelayGrpcService) negotiateNativeExecution(ctx context.Context, req *billingv1.ReserveQuotaRequest) error {
	if s.executionDelivery == nil {
		return fmt.Errorf("durable execution storage unavailable")
	}
	caps, err := s.billingClient.GetRoutingCapabilities(ctx, &billingv1.GetRoutingCapabilitiesRequest{})
	if err != nil {
		return err
	}
	if caps == nil || caps.ExecutionDeliveryVersion != 1 || caps.UsageContractVersion != 2 || caps.SettlementPolicyVersion != execution.Policy {
		return fmt.Errorf("billing native capability unavailable")
	}
	channel, err := s.channelClient.GetUsageCapabilities(ctx, &channelv1.GetUsageCapabilitiesRequest{})
	if err != nil {
		return err
	}
	if channel == nil || channel.UsageContractVersion != 2 {
		return fmt.Errorf("channel native capability unavailable")
	}
	req.ExecutionDeliveryVersion, req.UsageContractVersion, req.SettlementPolicyVersion = 1, 2, execution.Policy
	return nil
}
func (s *RelayGrpcService) admitNativeExecution(ctx context.Context, res *billingv1.ReserveQuotaResponse) (context.Context, *grpcDurableAttempt, error) {
	var i execution.Intent
	if res.ExecutionDeliveryVersion != 1 || res.UsageContractVersion != 2 || jsonx.Unmarshal(res.ExecutionIntentJson, &i) != nil || i.ReservationID != res.ReservationId {
		return ctx, nil, fmt.Errorf("durable intent mismatch")
	}
	if err := s.executionDelivery.Admit(ctx, execution.NewIntentEvent(i, time.Now())); err != nil {
		if !errors.Is(err, relaybiz.ErrExecutionAlreadyAdmitted) {
			call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_, _ = s.billingClient.ReleaseQuota(call, &billingv1.ReleaseQuotaRequest{ReservationId: res.ReservationId, Reason: "execution intent persistence failed"})
		}
		return ctx, nil, err
	}
	a := &grpcDurableAttempt{intent: i}
	ctx = evidence.WithDurableRecorder(ctx, func() error { a.mu.Lock(); defer a.mu.Unlock(); return a.err }, func(r evidence.Record) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.last = &r
		a.sequence++
		e := execution.Event{Version: 1, Intent: i, Category: "terminal", Sequence: a.sequence, ObservedAt: time.Now().Unix(), Record: &r, BuildRevision: evidence.BuildRevision}
		if e.Record != nil && e.Record.Execution == "checkpoint" {
			e.Category = "checkpoint"
		}
		e.ID = e.Key()
		call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		a.err = s.executionDelivery.Put(call, e)
		return a.err
	})
	return ctx, a, nil
}
func (s *RelayGrpcService) commitNativeExecution(ctx context.Context, a *grpcDurableAttempt, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return nil, 0, a.err
	}
	usage := relaybiz.NativeBillingUsage(billingdto.CommitUsage(req), a.last)
	native := relaybiz.NativeUsageV2(a.last)
	total := native.BillableTotal()
	raw, _ := jsonx.Marshal(usage)
	final, _ := jsonx.Marshal(billingdomain.SettlementPayload{ReservationID: req.ReservationId, ActualTokens: total, Success: true, Usage: raw})
	e := execution.Event{Version: 1, Intent: a.intent, Category: "final", Record: a.last, ObservedAt: time.Now().Unix(), Final: final, BuildRevision: evidence.BuildRevision}
	e.ID = e.Key()
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	r, err := s.executionDelivery.Deliver(call, e)
	if err != nil {
		return nil, total, err
	}
	_, _ = s.channelClient.RecordUsageSemanticVerdict(call, &channelv1.RecordUsageSemanticVerdictRequest{SourceKind: a.intent.SourceKind, SourceId: a.intent.SourceID, UpstreamModelId: a.intent.Model, AdapterProtocol: usage.Envelope.Protocol, ParseStatus: usage.Envelope.ParseStatus, Reason: usage.Envelope.DecisionReason})
	return &billingv1.CommitQuotaResponse{Success: r.SettlementAccepted, AsyncEnqueued: r.SettlementAccepted && !r.Settled, CommittedAmount: r.Amount}, total, nil
}
