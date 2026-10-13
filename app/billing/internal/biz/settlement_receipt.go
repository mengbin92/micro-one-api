package biz

import (
	"context"
	"os"

	billingv1 "micro-one-api/api/billing/v1"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/pkg/jsonx"

	"github.com/go-kratos/kratos/v3/errors"
)

var (
	ErrSettlementPayloadConflict    = errors.Conflict(billingv1.RoutingBillingErrorReason_SETTLEMENT_PAYLOAD_CONFLICT.String(), "settlement event payload conflicts with accepted content")
	ErrSettlementReceiptInvalid     = errors.BadRequest(billingv1.RoutingBillingErrorReason_SETTLEMENT_RECEIPT_INVALID.String(), "invalid settlement receipt contract")
	ErrSettlementReceiptUnavailable = errors.ServiceUnavailable(billingv1.RoutingBillingErrorReason_SETTLEMENT_RECEIPT_UNAVAILABLE.String(), "durable settlement receipts are unavailable")
	ErrSettlementReservationClosed  = errors.Conflict(billingv1.RoutingBillingErrorReason_SETTLEMENT_RESERVATION_CLOSED.String(), "reservation is closed to new settlement")
	ErrReservationExpiryDeferred    = errors.Conflict(billingv1.RoutingBillingErrorReason_RESERVATION_EXPIRY_DEFERRED.String(), "reservation expiry deferred after rechecking settlement or expiry")
)

const SettlementReceiptVersionV1 int32 = 1

type SettlementReceipt struct {
	Version                                          int32
	EventID, PayloadDigest, Status                   string
	Received, SettlementAccepted, Settled, Duplicate bool
	CommittedAmount                                  int64
}

// DurableSettlementTaskStore adds immutable intake without widening the
// existing recovery-store interface implemented by older consumers/tests.
type DurableSettlementTaskStore interface {
	SettlementTaskStore
	ReceiptsAvailable(context.Context) bool
	AcceptTask(context.Context, *SettleTask) (*SettlementReceipt, error)
}

func (t *SettleTask) PayloadDigest() (string, error) {
	usage, err := jsonx.Marshal(t.Usage)
	if err != nil {
		return "", err
	}
	return (billingdomain.SettlementPayload{ReservationID: t.ReservationID, ActualTokens: t.ActualTokens, Success: t.Success, SubscriptionAccountID: t.SubscriptionAccountID, Usage: usage}).Digest()
}

func (t *SettleTask) ValidateReceipt() error {
	if t == nil || t.ReservationID == "" || len(t.ReservationID) > 128 {
		return ErrSettlementReceiptInvalid
	}
	if t.AcceptedPayloadDigest != "" {
		digest, err := t.PayloadDigest()
		if err != nil || digest != t.AcceptedPayloadDigest {
			return ErrSettlementReceiptInvalid
		}
	}
	if t.SettlementReceiptVersion == 0 {
		if t.SettlementEventID != "" || t.SettlementPayloadDigest != "" {
			return ErrSettlementReceiptInvalid
		}
		return nil
	}
	if t.SettlementReceiptVersion != SettlementReceiptVersionV1 || !t.Success || t.ActualTokens < 0 || t.SettlementEventID != t.ReservationID+"/final/0" {
		return ErrSettlementReceiptInvalid
	}
	if t.Usage.UsageContractVersion != UsageContractVersionLegacy && t.Usage.UsageContractVersion != UsageContractVersionV1 {
		return ErrSettlementReceiptInvalid
	}
	if t.Usage.UsageContractVersion == UsageContractVersionV1 && validateV1Envelope(t.Usage.Envelope) != "" {
		return ErrSettlementReceiptInvalid
	}
	if hasNegative(t.Usage.PromptTokens, t.Usage.CompletionTokens, t.Usage.CacheReadTokens, t.Usage.CacheCreation5mTokens, t.Usage.CacheCreation1hTokens, t.Usage.UpstreamCost) {
		return ErrSettlementReceiptInvalid
	}
	digest, err := t.PayloadDigest()
	if err != nil || digest != t.SettlementPayloadDigest {
		return ErrSettlementReceiptInvalid
	}
	return nil
}

func (uc *AsyncBillingUsecase) DurableReceiptsAvailable(ctx context.Context) bool {
	if uc == nil || uc.syncUc == nil || os.Getenv("BILLING_SETTLEMENT_RECEIPTS_V1") != "true" {
		return false
	}
	store, ok := uc.getTaskStore().(DurableSettlementTaskStore)
	return ok && store.ReceiptsAvailable(ctx)
}

// AcceptSettlement only acknowledges the durable inbox. Recovery claims the
// original payload from storage; no sync fallback may invent a durable ACK.
func (uc *AsyncBillingUsecase) AcceptSettlement(ctx context.Context, task *SettleTask) (*SettlementReceipt, error) {
	if uc == nil || uc.syncUc == nil || os.Getenv("BILLING_SETTLEMENT_RECEIPTS_V1") != "true" {
		return nil, ErrSettlementReceiptUnavailable
	}
	store, ok := uc.getTaskStore().(DurableSettlementTaskStore)
	if !ok || !store.ReceiptsAvailable(ctx) {
		return nil, ErrSettlementReceiptUnavailable
	}
	if err := task.ValidateReceipt(); err != nil {
		return nil, err
	}
	if task.SettlementReceiptVersion != SettlementReceiptVersionV1 {
		return nil, ErrSettlementReceiptInvalid
	}
	intakeCtx, cancel := detachedSettleContext(ctx)
	defer cancel()
	receipt, err := store.AcceptTask(intakeCtx, task)
	if err == nil && receipt == nil {
		return nil, ErrSettlementReceiptUnavailable
	}
	return receipt, err
}
