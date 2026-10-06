package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

func (s *AdminService) ExportUsers(ctx context.Context, req *identityv1.ExportUsersRequest) (*identityv1.ExportUsersReply, error) {
	if s.identityClient == nil {
		return nil, status.Error(codes.Unavailable, "identity service unavailable")
	}
	return s.identityClient.ExportUsers(operatorRPCContext(ctx), req)
}
func (s *AdminService) ExportLedgerEntries(ctx context.Context, req *billingv1.ListLedgerRequest) (*billingv1.ExportArtifactReply, error) {
	if s.billingClient == nil {
		return nil, status.Error(codes.Unavailable, "billing service unavailable")
	}
	return s.billingClient.ExportLedgerEntries(operatorRPCContext(ctx), req)
}
