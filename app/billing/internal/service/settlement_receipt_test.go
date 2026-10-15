package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
)

type receiptTaskStore struct {
	available bool
	receipt   *biz.SettlementReceipt
	err       error
	calls     int
}

func (s *receiptTaskStore) SaveTask(context.Context, *biz.SettleTask) error {
	panic("receipt must use immutable intake")
}
func (s *receiptTaskStore) ListPending(context.Context, int) ([]*biz.SettleTask, error) {
	return nil, nil
}
func (s *receiptTaskStore) MarkCompleted(context.Context, string) error                 { return nil }
func (s *receiptTaskStore) MarkFailed(context.Context, string, string, time.Time) error { return nil }
func (s *receiptTaskStore) ReceiptsAvailable(context.Context) bool                      { return s.available }
func (s *receiptTaskStore) AcceptTask(_ context.Context, task *biz.SettleTask) (*biz.SettlementReceipt, error) {
	s.calls++
	if err := task.ValidateReceipt(); err != nil {
		return nil, err
	}
	return s.receipt, s.err
}
func receiptRequest(t *testing.T) *billingv1.CommitQuotaRequest {
	t.Helper()
	task := &biz.SettleTask{ReservationID: "receipt-res", ActualTokens: 10, Success: true, Usage: biz.LedgerUsage{PromptTokens: 10}}
	digest, err := task.PayloadDigest()
	require.NoError(t, err)
	return &billingv1.CommitQuotaRequest{ReservationId: task.ReservationID, ActualTokens: 10, Success: true, PromptTokens: 10, SettlementReceiptVersion: 1, SettlementEventId: "receipt-res/final/0", SettlementPayloadSha256: digest}
}
func receiptService(t *testing.T, store *receiptTaskStore) *BillingService {
	t.Helper()
	sync := biz.NewBillingUsecase(nil, nil, nil, nil, nil)
	async := biz.NewAsyncBillingUsecase(sync, nil, 1, 1, time.Hour)
	t.Cleanup(func() { require.NoError(t, async.Close()) })
	async.SetTaskStore(store)
	svc := NewBillingService(sync, nil, nil, nil)
	svc.SetAsyncBillingUsecase(async)
	return svc
}
func TestSettlementReceiptCapabilitiesFailClosed(t *testing.T) {
	store := &receiptTaskStore{available: true}
	svc := receiptService(t, store)
	t.Setenv("BILLING_SETTLEMENT_RECEIPTS_V1", "false")
	capabilities, err := svc.GetRoutingCapabilities(context.Background(), &billingv1.GetRoutingCapabilitiesRequest{})
	require.NoError(t, err)
	require.Zero(t, capabilities.SettlementReceiptVersion)
	_, err = svc.CommitQuota(context.Background(), receiptRequest(t))
	require.ErrorIs(t, err, biz.ErrSettlementReceiptUnavailable)
	require.Zero(t, store.calls)
	t.Setenv("BILLING_SETTLEMENT_RECEIPTS_V1", "true")
	store.available = false
	capabilities, err = svc.GetRoutingCapabilities(context.Background(), &billingv1.GetRoutingCapabilitiesRequest{})
	require.NoError(t, err)
	require.Zero(t, capabilities.SettlementReceiptVersion)
	store.available = true
	capabilities, err = svc.GetRoutingCapabilities(context.Background(), &billingv1.GetRoutingCapabilitiesRequest{})
	require.NoError(t, err)
	require.EqualValues(t, 1, capabilities.SettlementReceiptVersion)
}
func TestSettlementReceiptAckDoesNotClaimCompletion(t *testing.T) {
	t.Setenv("BILLING_SETTLEMENT_RECEIPTS_V1", "true")
	for _, status := range []string{"accepted", "processing", "settled", "released", "expired"} {
		t.Run(status, func(t *testing.T) {
			accepted := status != "released" && status != "expired"
			store := &receiptTaskStore{available: true, receipt: &biz.SettlementReceipt{Version: 1, EventID: "receipt-res/final/0", Status: status, Received: true, SettlementAccepted: accepted, Settled: status == "settled"}}
			svc := receiptService(t, store)
			result, err := svc.CommitQuota(context.Background(), receiptRequest(t))
			require.NoError(t, err)
			require.Equal(t, accepted, result.Success)
			require.Equal(t, accepted && status != "settled", result.AsyncEnqueued)
			require.Equal(t, status, result.SettlementReceipt.Status)
			require.Equal(t, status == "settled", result.SettlementReceipt.Settled)
			require.True(t, result.SettlementReceipt.Received)
			if !accepted {
				require.NotEmpty(t, result.ErrorMessage)
				require.Zero(t, result.CommittedAmount)
			}
		})
	}
}
func TestSettlementReceiptRejectsDigestAndVersionBeforeIntake(t *testing.T) {
	t.Setenv("BILLING_SETTLEMENT_RECEIPTS_V1", "true")
	store := &receiptTaskStore{available: true}
	svc := receiptService(t, store)
	for _, mutate := range []func(*billingv1.CommitQuotaRequest){
		func(r *billingv1.CommitQuotaRequest) { r.PromptTokens++ },
		func(r *billingv1.CommitQuotaRequest) { r.SettlementReceiptVersion = 2 },
		func(r *billingv1.CommitQuotaRequest) { r.SettlementEventId = "arbitrary-event" },
		func(r *billingv1.CommitQuotaRequest) { r.UsageContractVersion = 2 },
		func(r *billingv1.CommitQuotaRequest) { r.Success = false },
	} {
		request := receiptRequest(t)
		mutate(request)
		_, err := svc.CommitQuota(context.Background(), request)
		require.ErrorIs(t, err, biz.ErrSettlementReceiptInvalid)
	}
	require.Zero(t, store.calls)
}
