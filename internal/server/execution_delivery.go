package server

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/billingdto"
)

type durableAttempt struct {
	expiry     *time.Timer
	retire     sync.Once
	finalEvent *execution.Event
	mu         sync.Mutex
	intent     execution.Intent
	last       *evidence.Record
	sequence   int64
	err        error
	delivery   *relaybiz.ExecutionDelivery
}

func (s *HTTPServer) SetExecutionDelivery(d *relaybiz.ExecutionDelivery) { s.executionDelivery = d }
func executionDeliveryEnabled() bool                                     { return os.Getenv("RELAY_EXECUTION_DELIVERY_V1") == "true" }
func (s *HTTPServer) executionContext(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		requestID = requesttrace.FromContext(ctx).RootRequestID
	}
	value, ok := s.executionAttempts.Load(requestID)
	if !ok {
		return ctx
	}
	a := value.(*durableAttempt)
	ctx = evidence.WithObservationReader(ctx, func() *evidence.Record {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.last == nil {
			return nil
		}
		copy := *a.last
		return &copy
	})
	return evidence.WithDurableRecorder(ctx, func() error { a.mu.Lock(); defer a.mu.Unlock(); return a.err }, func(r evidence.Record) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.sequence++
		a.last = &r
		e := execution.Event{Version: 1, Intent: a.intent, Category: "terminal", Sequence: a.sequence, ObservedAt: time.Now().Unix(), Record: &r, BuildRevision: nativeShadowBuildID}
		if e.Record != nil && e.Record.Execution == "checkpoint" {
			e.Category = "checkpoint"
		}
		e.ID = e.Key()
		call, cancel := detachedBillingContext(ctx)
		defer cancel()
		if err := a.delivery.Put(call, e); err != nil {
			a.err = err
			return err
		}
		return nil
	})
}
func (s *HTTPServer) observeNativeUsage(ctx context.Context, ch *relaybiz.Channel, requestID, model string) context.Context {
	return withNativeUsageShadow(s.executionContext(ctx, requestID), ch, requestID, model)
}
func (s *HTTPServer) prepareDurableAttempt(ctx context.Context, resp *billingv1.ReserveQuotaResponse) error {
	if resp.ExecutionDeliveryVersion == 0 {
		return nil
	}
	if s.executionDelivery == nil || resp.ExecutionDeliveryVersion != 1 || resp.UsageContractVersion != 2 || resp.SettlementPolicyVersion != execution.Policy {
		return fmt.Errorf("execution capability mismatch")
	}
	var i execution.Intent
	if jsonx.Unmarshal(resp.ExecutionIntentJson, &i) != nil || i.ReservationID != resp.ReservationId || i.RequestID != resp.RequestId || i.Purpose != "user" {
		return fmt.Errorf("invalid persisted execution intent")
	}
	if s.executionActive.Add(1) > 10000 {
		s.executionActive.Add(-1)
		return fmt.Errorf("durable attempt capacity exhausted")
	}
	e := execution.NewIntentEvent(i, time.Now())
	if err := s.executionDelivery.Admit(ctx, e); err != nil {
		s.executionActive.Add(-1)
		return err
	}
	a := &durableAttempt{intent: i, delivery: s.executionDelivery}
	actual, loaded := s.executionAttempts.LoadOrStore(resp.RequestId, a)
	if loaded {
		a = actual.(*durableAttempt)
		s.executionActive.Add(-1)
		return relaybiz.ErrExecutionAlreadyAdmitted
	}
	s.executionAttempts.Store(resp.ReservationId, a)
	a.expiry = time.AfterFunc(time.Until(time.Unix(i.EvidenceDeadline, 0)), func() {
		a.retire.Do(func() {
			s.executionAttempts.Delete(i.RequestID)
			s.executionAttempts.Delete(i.ReservationID)
			s.executionActive.Add(-1)
		})
	})

	return nil
}
func nativeV2Envelope(r *evidence.Record) relaybiz.UsageEnvelope { return relaybiz.NativeUsageV2(r) }

func (s *HTTPServer) durableCommit(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	value, ok := s.executionAttempts.Load(req.ReservationId)
	if !ok {
		return nil, fmt.Errorf("execution intent missing")
	}
	a := value.(*durableAttempt)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return nil, a.err
	}
	u := nativeV2Envelope(a.last)
	req.UsageContractVersion = 2
	req.Usage = usageEnvelopeToProto(u)
	req.ActualTokens = u.BillableTotal()
	req.SourceKind = a.intent.SourceKind
	req.UpstreamModelId = a.intent.Model
	req.SubscriptionAccountId = 0
	if a.intent.SourceKind == "subscription" {
		req.SubscriptionAccountId = a.intent.SourceID
	}
	usage, err := jsonx.Marshal(relaybiz.NativeBillingUsage(billingdto.CommitUsage(req), a.last))
	if err != nil {
		return nil, err
	}
	p := billingdomain.SettlementPayload{ReservationID: req.ReservationId, ActualTokens: req.ActualTokens, Success: req.Success, SubscriptionAccountID: req.SubscriptionAccountId, Usage: usage}
	final, err := jsonx.Marshal(p)
	if err != nil {
		return nil, err
	}
	e := execution.Event{Version: 1, Intent: a.intent, Category: "final", Record: a.last, ObservedAt: time.Now().Unix(), Final: final, BuildRevision: nativeShadowBuildID}
	e.ID = e.Key()
	if a.finalEvent == nil {
		a.finalEvent = &e
	} else {
		e = *a.finalEvent
	}
	if a.expiry != nil {
		a.expiry.Reset(time.Minute)
	}
	r, err := a.delivery.Deliver(ctx, e)
	if err != nil {
		return nil, err
	}
	return &billingv1.CommitQuotaResponse{Success: r.SettlementAccepted, AsyncEnqueued: r.SettlementAccepted && !r.Settled, CommittedAmount: r.Amount, SettlementReceipt: &billingv1.SettlementReceipt{Version: 1, EventId: e.ID, PayloadSha256: r.Digest, Received: r.Received, SettlementAccepted: r.SettlementAccepted, Settled: r.Settled, Status: r.Status}}, nil
}

func executionPilot(kind, channel string, account int64, model string) bool {
	if !executionDeliveryEnabled() {
		return false
	}
	id := channel
	if kind == "subscription" {
		id = strconv.FormatInt(account, 10)
	}
	key := kind + ":" + id + ":" + model
	for _, allowed := range strings.Split(os.Getenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST"), ",") {
		if strings.TrimSpace(allowed) == key {
			return true
		}
	}
	return false
}
