package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"

	"github.com/stretchr/testify/require"

	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/app/config/internal/biz"
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

func writeAuditSystemContext(method string) context.Context {
	ctx := authorization.WithExternal(context.Background())
	ctx = serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "admin", Dedicated: true})
	return serviceidentity.WithRPCMethod(ctx, method)
}

func TestListWriteAuditsRequiresAdminSystemCapability(t *testing.T) {
	svc := NewConfigService(nil)
	svc.SetWriteAuditUsecase(biz.NewWriteAuditUsecase(&writeAuditRepoStub{}))
	ctx := authorization.WithExternal(context.Background())
	_, err := svc.ListWriteAudits(ctx, &configv1.ListWriteAuditsRequest{PageSize: 10})
	require.Error(t, err)
}

func TestListWriteAuditsRejectsForwardedOperator(t *testing.T) {
	svc := NewConfigService(nil)
	svc.SetWriteAuditUsecase(biz.NewWriteAuditUsecase(&writeAuditRepoStub{}))
	ctx := authorization.WithCredential(writeAuditSystemContext("/api.config.v1.ConfigService/ListWriteAudits"), "user-session")
	_, err := svc.ListWriteAudits(ctx, &configv1.ListWriteAuditsRequest{PageSize: 10})
	require.ErrorIs(t, err, authorization.ErrDenied)
}

func TestListWriteAuditsConvertsRows(t *testing.T) {
	repo := &writeAuditRepoStub{items: []biz.WriteAudit{{
		EventID: "ev-9", ActorUserID: 3, Operation: "config.model_mapping.create",
		ResourceID: "12", DecisionVersions: `[2]`, Reason: "ops", OccurredAt: 77, Result: "success",
	}}, total: 1}
	svc := NewConfigService(nil)
	svc.SetWriteAuditUsecase(biz.NewWriteAuditUsecase(repo))
	resp, err := svc.ListWriteAudits(writeAuditSystemContext("/api.config.v1.ConfigService/ListWriteAudits"), &configv1.ListWriteAuditsRequest{PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, resp.Total)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "ev-9", resp.Items[0].EventId)
	require.Equal(t, "config.model_mapping.create", resp.Items[0].Operation)
	require.EqualValues(t, 77, resp.Items[0].OccurredAt)
}

func TestListWriteAuditsUnavailableWithoutUsecase(t *testing.T) {
	svc := NewConfigService(nil)
	_, err := svc.ListWriteAudits(writeAuditSystemContext("/api.config.v1.ConfigService/ListWriteAudits"), &configv1.ListWriteAuditsRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestListWriteAuditsMapsStorageFailure(t *testing.T) {
	svc := NewConfigService(nil)
	svc.SetWriteAuditUsecase(biz.NewWriteAuditUsecase(&writeAuditRepoStub{err: biz.ErrWriteAuditUnavailable}))
	_, err := svc.ListWriteAudits(writeAuditSystemContext("/api.config.v1.ConfigService/ListWriteAudits"), &configv1.ListWriteAuditsRequest{PageSize: 10})
	require.Equal(t, codes.Unavailable, status.Code(err))
}
