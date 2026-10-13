package biz

import (
	"context"
	"os"
	"strconv"
	"time"

	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/security/serviceidentity"
)

type ExecutionRepo interface {
	ExecutionAvailable(context.Context) bool
	StoredExecutionIntent(context.Context, string) (execution.Intent, error)
	AcceptExecution(context.Context, execution.Event, string) (execution.Receipt, error)
}
type executionIntentKey struct{}

func ExecutionIntent(ctx context.Context) *execution.Intent {
	i, _ := ctx.Value(executionIntentKey{}).(*execution.Intent)
	return i
}
func (uc *BillingUsecase) ExecutionAvailable(ctx context.Context) bool {
	r, ok := uc.reservationRepo.(ExecutionRepo)
	return os.Getenv("BILLING_EXECUTION_DELIVERY_V1") == "true" && uc.RoutingSnapshotsAvailable() && ok && r.ExecutionAvailable(ctx)
}
func (uc *BillingUsecase) PrepareExecution(ctx context.Context, requestID, channelID string, accountID int64) (context.Context, error) {
	if !uc.ExecutionAvailable(ctx) {
		return ctx, ErrSettlementReceiptUnavailable
	}
	t := requesttrace.FromContext(ctx)
	id, err := strconv.ParseInt(channelID, 10, 64)
	if t.SourceKind == "subscription" {
		id = accountID
		err = nil
	}
	if err != nil || id <= 0 || t.UpstreamModelID == "" {
		return ctx, ErrSettlementReceiptInvalid
	}
	now := uc.Now()
	i := &execution.Intent{RequestID: requestID, RootRequestID: t.RootRequestID, AttemptNumber: t.Number, Purpose: "user", SourceKind: t.SourceKind, SourceID: id, Model: t.UpstreamModelID, Policy: execution.Policy, LeaseUntil: now.Add(5 * time.Minute).Unix(), EvidenceDeadline: now.Add(24 * time.Hour).Unix(), CostBasis: "unknown"}
	frozen := execution.Freeze(*i)
	i = &frozen
	// Only an exact source/model tariff is procurement evidence; no public-model fallback.
	if price, ok := uc.pricingConfig(ctx).UpstreamPrices[canonicalUpstreamPriceKey(i.SourceKind, id, i.Model)]; ok {
		i.Tariff, err = jsonx.Marshal(price)
		if err != nil {
			return ctx, err
		}
		i.CostBasis = "tariff_estimate"
	}
	return context.WithValue(ctx, executionIntentKey{}, i), nil
}
func (uc *BillingUsecase) AcceptExecution(ctx context.Context, e execution.Event, digest string) (execution.Receipt, error) {
	const method = "/api.billing.v1.BillingService/RecordExecutionEvidence"
	if !serviceidentity.HasSystemCapability(ctx, method) {
		return execution.Receipt{}, ErrSettlementReceiptInvalid
	}
	repo, ok := uc.reservationRepo.(ExecutionRepo)
	if !ok || !repo.ExecutionAvailable(ctx) {
		return execution.Receipt{}, ErrSettlementReceiptUnavailable
	}
	_, expected, err := e.Encode()
	if err != nil || digest != expected {
		return execution.Receipt{}, ErrSettlementReceiptInvalid
	}
	return uc.reservationRepo.(ExecutionRepo).AcceptExecution(ctx, e, digest)
}

// SupplierEstimate is a configured tariff estimate, never a supplier invoice.
func SupplierEstimate(i execution.Intent, r *evidence.Record) *int64 {
	if i.CostBasis != "tariff_estimate" || r == nil || r.Observation.Status != "verified" || r.Observation.Buckets == nil {
		return nil
	}
	var price ModelPrice
	if jsonx.Unmarshal(i.Tariff, &price) != nil {
		return nil
	}
	b := r.Observation.Buckets
	if b[1] > 0 && price.CacheReadPrice == nil || b[2] > 0 && price.CacheCreation5mPrice == nil || b[3] > 0 && price.CacheCreation1hPrice == nil {
		return nil
	}
	value := calculateCanonicalCost(price, CanonicalBuckets{UncachedInputTokens: b[0], CacheReadTokens: b[1], CacheCreation5mTokens: b[2], CacheCreation1hTokens: b[3], OutputTokens: b[4]}, 1).CanonicalCost
	return &value
}

func ValidateNativeFinal(i execution.Intent, r *evidence.Record, u LedgerUsage) bool {
	if u.Envelope == nil || u.Envelope.ParseStatus != "verified" {
		return u.Envelope != nil && u.Envelope.Canonical == nil
	}
	if r == nil || !r.Complete || r.Observation.Buckets == nil || r.Observation.Status != "verified" || r.Observation.Protocol != u.Envelope.Protocol {
		return false
	}
	key := "usage"
	if r.Observation.Protocol == "gemini" {
		key = "usageMetadata"
	}
	body, _ := jsonx.Marshal(map[string]any{key: r.Observation.Usage})
	native := evidence.Parse(body, r.Observation.Protocol)
	if native.Status != "verified" || native.Buckets == nil || native.Semantics != u.Envelope.Semantics || native.ParserRevision != i.ParserRevision || native.ContractRevision != i.ContractRevision {
		return false
	}
	b := u.Envelope.Canonical
	return b != nil && *native.Buckets == [5]int64{b.UncachedInputTokens, b.CacheReadTokens, b.CacheCreation5mTokens, b.CacheCreation1hTokens, b.OutputTokens}
}

func (uc *BillingUsecase) StoredExecutionIntent(ctx context.Context, id string) (execution.Intent, error) {
	r, ok := uc.reservationRepo.(ExecutionRepo)
	if !ok {
		return execution.Intent{}, ErrSettlementReceiptUnavailable
	}
	return r.StoredExecutionIntent(ctx, id)
}
