package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"

	"github.com/stretchr/testify/require"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

type writeAuditRepoStub struct {
	items []biz.WriteAudit
	total int64
	err   error
}

func (s *writeAuditRepoStub) ListWriteAudits(context.Context, biz.WriteAuditFilter) (biz.WriteAuditPage, error) {
	return biz.WriteAuditPage{Items: s.items, Total: s.total, StoreID: "test-store"}, s.err
}

func newWriteAuditTestService(repo biz.WriteAuditRepo) *BillingService {
	uc := biz.NewBillingUsecase(nil, nil, nil, nil, nil)
	uc.SetWriteAuditRepo(repo)
	return NewBillingService(uc, nil, nil, nil)
}

func writeAuditSystemContext(method string) context.Context {
	ctx := authorization.WithExternal(context.Background())
	ctx = serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "admin", Dedicated: true})
	return serviceidentity.WithRPCMethod(ctx, method)
}

func TestListWriteAuditsRequiresAdminSystemCapability(t *testing.T) {
	svc := newWriteAuditTestService(&writeAuditRepoStub{})
	// External request carrying an operator credential but no system
	// capability must not reach the repo.
	ctx := authorization.WithExternal(context.Background())
	_, err := svc.ListWriteAudits(ctx, &billingv1.ListWriteAuditsRequest{PageSize: 10})
	require.Error(t, err)
}

func TestListWriteAuditsRejectsForwardedOperator(t *testing.T) {
	svc := newWriteAuditTestService(&writeAuditRepoStub{})
	ctx := authorization.WithCredential(writeAuditSystemContext("/api.billing.v1.BillingService/ListWriteAudits"), "user-session")
	_, err := svc.ListWriteAudits(ctx, &billingv1.ListWriteAuditsRequest{PageSize: 10})
	require.ErrorIs(t, err, authorization.ErrDenied)
}

func TestListWriteAuditsConvertsRows(t *testing.T) {
	repo := &writeAuditRepoStub{items: []biz.WriteAudit{{
		EventID: "ev-1", ActorUserID: 7, Operation: "billing.vendor.import",
		ResourceID: "9", DecisionVersions: `[1]`, Reason: "ops", OccurredAt: 42, Result: "success",
	}}, total: 1}
	svc := newWriteAuditTestService(repo)
	resp, err := svc.ListWriteAudits(writeAuditSystemContext("/api.billing.v1.BillingService/ListWriteAudits"), &billingv1.ListWriteAuditsRequest{PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, resp.Total)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "ev-1", resp.Items[0].EventId)
	require.Equal(t, "billing.vendor.import", resp.Items[0].Operation)
	require.EqualValues(t, 42, resp.Items[0].OccurredAt)
	require.Equal(t, "success", resp.Items[0].Result)
}

func TestListWriteAuditsUnavailableWithoutRepo(t *testing.T) {
	svc := NewBillingService(biz.NewBillingUsecase(nil, nil, nil, nil, nil), nil, nil, nil)
	_, err := svc.ListWriteAudits(writeAuditSystemContext("/api.billing.v1.BillingService/ListWriteAudits"), &billingv1.ListWriteAuditsRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestListWriteAuditsMapsStorageFailure(t *testing.T) {
	svc := newWriteAuditTestService(&writeAuditRepoStub{err: biz.ErrWriteAuditUnavailable})
	_, err := svc.ListWriteAudits(writeAuditSystemContext("/api.billing.v1.BillingService/ListWriteAudits"), &billingv1.ListWriteAuditsRequest{PageSize: 10})
	require.Equal(t, codes.Unavailable, status.Code(err))
}
