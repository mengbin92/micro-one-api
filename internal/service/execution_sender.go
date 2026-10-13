package service

import (
	"context"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/execution"
	"micro-one-api/internal/biz"
)

type executionSender struct {
	client billingv1.BillingServiceClient
}

func NewExecutionSender(c billingv1.BillingServiceClient) biz.ExecutionSender {
	return &executionSender{c}
}
func (s *executionSender) Send(ctx context.Context, e execution.Event) (execution.Receipt, error) {
	b, d, err := e.Encode()
	if err != nil {
		return execution.Receipt{}, err
	}
	r, err := s.client.RecordExecutionEvidence(ctx, &billingv1.RecordExecutionEvidenceRequest{EventJson: b, PayloadSha256: d})
	if err != nil {
		return execution.Receipt{}, err
	}
	if r == nil {
		return execution.Receipt{}, biz.ErrExecutionReceipt
	}
	return execution.Receipt{Received: r.Received, SettlementAccepted: r.SettlementAccepted, Settled: r.Settled, Status: r.Status, Digest: r.PayloadSha256, Amount: r.CommittedAmount, Comparison: r.RealTrafficComparisonJson}, nil
}
