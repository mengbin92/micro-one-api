package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	adminbiz "micro-one-api/app/admin/internal/biz"
)

type operationAuditServiceRepo struct {
	items []adminbiz.OperationAudit
	total int64
}

func (r *operationAuditServiceRepo) List(context.Context, adminbiz.OperationAuditFilter) (adminbiz.OperationAuditBatch, error) {
	return adminbiz.OperationAuditBatch{Items: r.items, Total: r.total, StoreID: "test-store"}, nil
}

type operationAuditIdentityClient struct {
	identityv1.IdentityServiceClient
	names map[int64]string
	err   error
	calls int
}

func (c *operationAuditIdentityClient) GetUser(_ context.Context, req *identityv1.GetUserRequest, _ ...grpc.CallOption) (*identityv1.GetUserReply, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &identityv1.GetUserReply{User: &commonv1.UserInfo{Id: req.UserId, Username: c.names[req.UserId]}}, nil
}

func TestListOperationAuditsResolvesDistinctActorUsernames(t *testing.T) {
	repo := &operationAuditServiceRepo{
		items: []adminbiz.OperationAudit{
			{EventID: "a", ActorUserID: 7},
			{EventID: "b", ActorUserID: 7},
			{EventID: "c", ActorUserID: 9},
		},
		total: 3,
	}
	identity := &operationAuditIdentityClient{names: map[int64]string{7: "admin", 9: "auditor"}}
	svc := NewAdminService(nil, identity, nil, nil)
	svc.SetOperationAuditUsecase(adminbiz.NewOperationAuditUsecase(repo))

	page, err := svc.ListOperationAudits(context.Background(), adminbiz.OperationAuditFilter{}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 2, identity.calls, "one identity lookup per distinct actor")
	require.Equal(t, []string{"admin", "admin", "auditor"}, []string{
		page.Items[0].ActorUsername, page.Items[1].ActorUsername, page.Items[2].ActorUsername,
	})
}

// blockingAuditIdentityClient simulates a slow identity service that only
// returns when the caller's context expires.
type blockingAuditIdentityClient struct {
	identityv1.IdentityServiceClient
	calls int
}

func (c *blockingAuditIdentityClient) GetUser(ctx context.Context, req *identityv1.GetUserRequest, _ ...grpc.CallOption) (*identityv1.GetUserReply, error) {
	c.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestListOperationAuditsSharesLookupBudgetAcrossActors(t *testing.T) {
	repo := &operationAuditServiceRepo{
		items: []adminbiz.OperationAudit{
			{EventID: "a", ActorUserID: 7},
			{EventID: "b", ActorUserID: 9},
			{EventID: "c", ActorUserID: 11},
		},
		total: 3,
	}
	identity := &blockingAuditIdentityClient{}
	svc := NewAdminService(nil, identity, nil, nil)
	svc.SetOperationAuditUsecase(adminbiz.NewOperationAuditUsecase(repo))

	start := time.Now()
	page, err := svc.ListOperationAudits(context.Background(), adminbiz.OperationAuditFilter{}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, identity.calls, "once the shared budget is spent the loop must stop")
	require.Less(t, time.Since(start), 3*time.Second, "enrichment must stay within one shared budget, not one per actor")
	for _, item := range page.Items {
		require.Empty(t, item.ActorUsername, "expired lookups degrade to the numeric id")
	}
}

func TestListOperationAuditsFallsBackWhenActorLookupDenied(t *testing.T) {
	repo := &operationAuditServiceRepo{
		items: []adminbiz.OperationAudit{{EventID: "a", ActorUserID: 7}, {EventID: "b", ActorUserID: 9}},
		total: 2,
	}
	identity := &operationAuditIdentityClient{err: status.Error(codes.PermissionDenied, "denied")}
	svc := NewAdminService(nil, identity, nil, nil)
	svc.SetOperationAuditUsecase(adminbiz.NewOperationAuditUsecase(repo))

	page, err := svc.ListOperationAudits(context.Background(), adminbiz.OperationAuditFilter{}, 1, 20)
	require.NoError(t, err, "audit reads must survive a denied user lookup")
	require.Equal(t, 1, identity.calls, "stop after the first permission denial")
	require.Empty(t, page.Items[0].ActorUsername)
	require.Empty(t, page.Items[1].ActorUsername)
}
