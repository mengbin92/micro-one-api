package service

import (
	"bytes"
	"context"
	v "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
	"time"
)

func (s *BillingService) ImportVendorReport(ctx context.Context, p *v.ImportVendorReportRequest) (*v.ImportVendorReportReply, error) {
	var m biz.VendorManifest
	if err := biz.DecodeVendorJSON(p.GetManifestJson(), &m); err != nil {
		return nil, err
	}
	if len(p.GetCsv()) > 2<<20 {
		return nil, biz.ErrSettlementReceiptInvalid
	}
	if len(p.GetCostEvidenceJson()) > 0 && len(p.GetCsv()) > 0 {
		return nil, biz.ErrSettlementReceiptInvalid
	}
	var attrs map[string][]biz.VendorAttribution
	if len(p.GetAttributionsJson()) > 0 {
		if err := biz.DecodeVendorJSON(p.GetAttributionsJson(), &attrs); err != nil {
			return nil, err
		}
	}
	var report biz.VendorReconciliation
	var err error
	if len(p.GetCostEvidenceJson()) > 0 {
		var archive biz.VendorCostEvidence
		if err := biz.DecodeVendorJSON(p.GetCostEvidenceJson(), &archive); err != nil {
			return nil, err
		}
		report, err = s.uc.ImportVendorCostReport(ctx, m, archive, p.GetExpectedReportId(), p.GetReason(), attrs)
	} else {
		rows, readErr := biz.ReadVendorCSV(bytes.NewReader(p.GetCsv()), m.Scope)
		if readErr != nil {
			return nil, readErr
		}
		report, err = s.uc.ImportVendorReport(ctx, m, rows, p.GetExpectedReportId(), p.GetReason(), attrs)
	}
	if err != nil {
		return nil, err
	}
	raw, err := jsonx.Marshal(report)
	return &v.ImportVendorReportReply{ReportId: report.ID, ReportJson: raw}, err
}
func (s *BillingService) ListVendorReports(ctx context.Context, p *v.ListVendorReportsRequest) (*v.ListVendorReportsReply, error) {
	rows, daily, err := s.uc.ListVendorReports(ctx, time.Unix(p.GetStart(), 0), time.Unix(p.GetEnd(), 0), int(p.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &v.ListVendorReportsReply{}
	for _, r := range rows {
		raw, err := jsonx.Marshal(r)
		if err != nil {
			return nil, err
		}
		out.ReportsJson = append(out.ReportsJson, raw)
	}
	out.DailySummaryJson, err = jsonx.Marshal(daily)
	if err != nil {
		return nil, err
	}
	out.WeeklyReviewJson, err = jsonx.Marshal(biz.SummarizeVendorWeekly(rows, time.Unix(p.GetEnd(), 0)))
	return out, err
}
