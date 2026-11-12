package service

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "micro-one-api/api/identity/v1"
	adminbiz "micro-one-api/app/admin/internal/biz"
)

// auditActorLookupTimeout bounds the identity hop per distinct actor so a slow
// or unavailable identity service cannot stall the audit read.
const auditActorLookupTimeout = 2 * time.Second

func (s *AdminService) SetOperationAuditUsecase(uc *adminbiz.OperationAuditUsecase) {
	if s != nil {
		s.operationAuditUc = uc
	}
}

func (s *AdminService) ListOperationAudits(ctx context.Context, filter adminbiz.OperationAuditFilter, page, pageSize int) (adminbiz.OperationAuditPage, error) {
	if s == nil || s.operationAuditUc == nil {
		return adminbiz.OperationAuditPage{}, adminbiz.ErrOperationAuditUnavailable
	}
	if err := s.AuthorizeSection(ctx, "admin.business_audits", true, "iam.audit.read"); err != nil {
		return adminbiz.OperationAuditPage{}, err
	}
	result, err := s.operationAuditUc.List(ctx, filter, page, pageSize)
	if err != nil {
		return adminbiz.OperationAuditPage{}, err
	}
	s.fillActorUsernames(ctx, result.Items)
	return result, nil
}

// fillActorUsernames enriches audit rows with the actor's login name. Names
// live in the identity schema while the shared audit table only stores
// actor_user_id, so resolution is a best-effort read per distinct actor. A
// missing user-read permission, a deleted account or an identity outage must
// degrade to the numeric id, never fail the audit page.
func (s *AdminService) fillActorUsernames(ctx context.Context, items []adminbiz.OperationAudit) {
	if s == nil || s.identityClient == nil || len(items) == 0 {
		return
	}
	names := make(map[int64]string, len(items))
	for i := range items {
		id := items[i].ActorUserID
		if id <= 0 {
			continue
		}
		name, cached := names[id]
		if !cached {
			callCtx, cancel := context.WithTimeout(operatorRPCContext(ctx), auditActorLookupTimeout)
			reply, err := s.identityClient.GetUser(callCtx, &identityv1.GetUserRequest{UserId: id})
			cancel()
			switch {
			case err != nil:
				if code := status.Code(err); code == codes.PermissionDenied || code == codes.Unauthenticated {
					// Every remaining row would fail the same way; stop early and
					// let the caller show numeric ids.
					return
				}
				name = ""
			case reply.GetUser() != nil:
				name = reply.GetUser().GetUsername()
			}
			names[id] = name
		}
		items[i].ActorUsername = name
	}
}
