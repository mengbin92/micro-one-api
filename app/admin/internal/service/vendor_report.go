package service

import (
	"context"
	v "micro-one-api/api/billing/v1"
)

func (s *AdminService) ImportVendorReport(ctx context.Context, p *v.ImportVendorReportRequest) (*v.ImportVendorReportReply, error) {
	return s.billingClient.ImportVendorReport(operatorRPCContext(ctx), p)
}
func (s *AdminService) ListVendorReports(ctx context.Context, p *v.ListVendorReportsRequest) (*v.ListVendorReportsReply, error) {
	return s.billingClient.ListVendorReports(operatorRPCContext(ctx), p)
}
