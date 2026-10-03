package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
)

func (s *AdminService) ExportCostReport(ctx context.Context, req *billingv1.ExportCostReportRequest) (*billingv1.ExportArtifactReply, error) {
	if s.billingClient == nil {
		return nil, status.Error(codes.Unavailable, "billing service unavailable")
	}
	return s.billingClient.ExportCostReport(operatorRPCContext(ctx), req)
}
func (s *AdminService) ExportRedeemCodes(ctx context.Context) (*billingv1.ExportArtifactReply, error) {
	if s.billingClient == nil {
		return nil, status.Error(codes.Unavailable, "billing service unavailable")
	}
	return s.billingClient.ExportRedeemCodes(operatorRPCContext(ctx), &billingv1.ExportRedeemCodesRequest{})
}
func (s *AdminService) RunReconciliation(ctx context.Context, reason string) (*billingv1.GetReconciliationRunResponse, error) {
	if s.billingClient == nil {
		return nil, status.Error(codes.Unavailable, "billing service unavailable")
	}
	return s.billingClient.RunReconciliation(operatorRPCContext(ctx), &billingv1.RunReconciliationRequest{Reason: reason})
}

func (s *AdminService) ResetAccountBalance(ctx context.Context, req *billingv1.ResetAccountBalanceRequest) (*billingv1.TopUpQuotaResponse, error) {
	if s.billingClient == nil {
		return nil, status.Error(codes.Unavailable, "billing service unavailable")
	}
	return s.billingClient.ResetAccountBalance(operatorRPCContext(ctx), req)
}
