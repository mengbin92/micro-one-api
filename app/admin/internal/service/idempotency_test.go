package service

import (
	"context"
	"strings"
	"testing"

	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// recordingPurchaseBillingClient records every purchase/topup RPC so tests can
// assert the Idempotency-Key was forwarded to billing as request_id (v0.18
// P0). Embedded interface: unimplemented methods panic if invoked.
// purchaseErr / topupErr, when non-nil, are returned as gRPC errors (used to
// simulate the billing-side AlreadyExists idempotency conflict).
type recordingPurchaseBillingClient struct {
	billingv1.BillingServiceClient
	purchaseReqs []*billingv1.PurchaseSubscriptionRequest
	topupReqs    []*billingv1.TopUpQuotaRequest
	purchaseErr  error
	topupErr     error
}

func (m *recordingPurchaseBillingClient) PurchaseSubscription(_ context.Context, req *billingv1.PurchaseSubscriptionRequest, _ ...grpc.CallOption) (*billingv1.PurchaseSubscriptionResponse, error) {
	m.purchaseReqs = append(m.purchaseReqs, req)
	if m.purchaseErr != nil {
		return nil, m.purchaseErr
	}
	return &billingv1.PurchaseSubscriptionResponse{Success: true, NewBalance: 9000}, nil
}

func (m *recordingPurchaseBillingClient) TopUpQuota(_ context.Context, req *billingv1.TopUpQuotaRequest, _ ...grpc.CallOption) (*billingv1.TopUpQuotaResponse, error) {
	m.topupReqs = append(m.topupReqs, req)
	if m.topupErr != nil {
		return nil, m.topupErr
	}
	return &billingv1.TopUpQuotaResponse{Success: true, NewBalance: 9000}, nil
}

func newRecordingSvc(billing *recordingPurchaseBillingClient) *AdminService {
	group := &subscriptionbiz.SubscriptionGroup{
		ID: 5, Name: "G5", DisplayName: "G5",
		Status:     subscriptionbiz.SubscriptionGroupStatusEnabled,
		PriceQuota: 1000, DurationDays: 30,
	}
	groupRepo := &fakeGroupRepo{group: group}
	subRepo := &fakeSubscriptionRepo{}
	svc := NewAdminService(billing, nil, nil, nil)
	svc.SetSubscriptionUsecases(
		subscriptionbiz.NewSubscriptionUsecase(subRepo, groupRepo),
		subscriptionbiz.NewGroupUsecase(groupRepo),
	)
	return svc
}

// TestPurchaseSubscription_ForwardsRequestID verifies the admin purchase path
// forwards the client's Idempotency-Key to billing as request_id so the DB
// unique constraint can reject a concurrent duplicate (P0 acceptance seam).
func TestPurchaseSubscription_ForwardsRequestID(t *testing.T) {
	billing := &recordingPurchaseBillingClient{}
	svc := newRecordingSvc(billing)

	_, err := svc.PurchaseSubscription(context.Background(), 42, 5, "idem-key-1")
	require.NoError(t, err)
	require.Len(t, billing.purchaseReqs, 1)
	require.Equal(t, "idem-key-1", billing.purchaseReqs[0].GetRequestId())
}

// TestPurchaseSubscription_EmptyRequestIDGetsAutoKey verifies the
// legacy-client path: no Idempotency-Key yields an auto key (distinct per
// call, never colliding with legacy rows; no idempotency guarantee).
func TestPurchaseSubscription_EmptyRequestIDGetsAutoKey(t *testing.T) {
	billing := &recordingPurchaseBillingClient{}
	svc := newRecordingSvc(billing)

	_, err := svc.PurchaseSubscription(context.Background(), 42, 5, "")
	require.NoError(t, err)
	require.Len(t, billing.purchaseReqs, 1)
	got := billing.purchaseReqs[0].GetRequestId()
	if !strings.HasPrefix(got, "auto:") {
		t.Fatalf("empty key must yield auto key, got %q", got)
	}
}

// TestPurchaseSubscription_InvalidKeyRejected verifies an over-long or
// non-printable Idempotency-Key is rejected before it reaches billing.
func TestPurchaseSubscription_InvalidKeyRejected(t *testing.T) {
	billing := &recordingPurchaseBillingClient{}
	svc := newRecordingSvc(billing)

	_, err := svc.PurchaseSubscription(context.Background(), 42, 5, strings.Repeat("k", 200))
	require.ErrorIs(t, err, ErrInvalidIdempotencyKey)

	_, err = svc.PurchaseSubscription(context.Background(), 42, 5, "bad\nkey")
	require.ErrorIs(t, err, ErrInvalidIdempotencyKey)

	require.Len(t, billing.purchaseReqs, 0, "invalid keys must not reach billing")
}

// TestChangeSubscription_ForwardsRequestID verifies the upgrade-difference
// charge carries the same Idempotency-Key so concurrent upgrade replays cannot
// double-charge (the second charge is rejected by billing's unique constraint).
func TestChangeSubscription_ForwardsRequestID(t *testing.T) {
	billing := &recordingPurchaseBillingClient{}
	group := &subscriptionbiz.SubscriptionGroup{
		ID: 5, Name: "G5", DisplayName: "G5",
		Status:     subscriptionbiz.SubscriptionGroupStatusEnabled,
		PriceQuota: 1000, DurationDays: 30,
	}
	groupRepo := &fakeGroupRepo{group: group}
	subRepo := &fakeSubscriptionRepo{active: &subscriptionbiz.UserSubscription{
		ID: 1, UserID: 42, GroupID: 5,
		Status: subscriptionbiz.SubscriptionStatusActive,
	}}
	svc := NewAdminService(billing, nil, nil, nil)
	svc.SetSubscriptionUsecases(
		subscriptionbiz.NewSubscriptionUsecase(subRepo, groupRepo),
		subscriptionbiz.NewGroupUsecase(groupRepo),
	)

	_, err := svc.ChangeSubscription(context.Background(), subscriptionbiz.ChangeRequest{
		UserID:             42,
		FromSubscriptionID: 1,
		ToGroupID:          5,
		ToPlanID:           0,
		NewPriceQuota:      2000,
		OldPriceQuota:      1000,
		Policy:             subscriptionbiz.SubscriptionChangePolicyImmediate,
		Operator:           "admin-1",
	}, "idem-upgrade-1")
	require.NoError(t, err)
	require.Len(t, billing.purchaseReqs, 1, "immediate upgrade must charge the difference")
	require.Equal(t, "idem-upgrade-1", billing.purchaseReqs[0].GetRequestId())
	require.Equal(t, int64(1000), billing.purchaseReqs[0].GetPriceAmount())
}

// TestNormalizeRequestID validates the key normalization contract.
func TestNormalizeRequestID(t *testing.T) {
	// valid keys pass through
	got, err := normalizeRequestID("order-123")
	require.NoError(t, err)
	require.Equal(t, "order-123", got)

	// empty yields distinct auto keys
	k1, err := normalizeRequestID("")
	require.NoError(t, err)
	k2, err := normalizeRequestID("")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(k1, "auto:") && k1 != k2)

	// over-long rejected
	_, err = normalizeRequestID(strings.Repeat("k", 101))
	require.ErrorIs(t, err, ErrInvalidIdempotencyKey)

	// non-printable rejected
	_, err = normalizeRequestID("a\x00b")
	require.ErrorIs(t, err, ErrInvalidIdempotencyKey)
}

// TestTopUpQuota_DuplicateSurfacesAsAlreadyExists guards v0.18 P0 §5.4 on the
// admin side: when billing reports an idempotency conflict (gRPC
// AlreadyExists), the admin service must pass it through as a gRPC error (so
// the HTTP layer maps it to 409) instead of swallowing it into a
// Success:false business response (HTTP 200).
func TestTopUpQuota_DuplicateSurfacesAsAlreadyExists(t *testing.T) {
	billing := &recordingPurchaseBillingClient{
		topupErr: status.Error(codes.AlreadyExists, "duplicate request"),
	}
	svc := NewAdminService(billing, nil, nil, nil)

	_, err := svc.TopUpQuota(context.Background(), &adminv1.TopUpQuotaRequest{
		UserId: "42", Amount: 500, OperatorId: "admin", Remark: "topup",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "duplicate must surface as a gRPC status error, got %v", err)
	require.Equal(t, codes.AlreadyExists, st.Code())
}

// TestPurchaseSubscription_DuplicateSurfacesAsAlreadyExists is the same §5.4
// guard for the wallet-purchase path: the gRPC AlreadyExists from billing must
// propagate (HTTP layer maps to 409).
func TestPurchaseSubscription_DuplicateSurfacesAsAlreadyExists(t *testing.T) {
	billing := &recordingPurchaseBillingClient{
		purchaseErr: status.Error(codes.AlreadyExists, "duplicate request"),
	}
	svc := newRecordingSvc(billing)

	_, err := svc.PurchaseSubscription(context.Background(), 42, 5, "idem-key-1")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "duplicate must surface as a gRPC status error, got %v", err)
	require.Equal(t, codes.AlreadyExists, st.Code())
}
