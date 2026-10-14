package biz

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"strconv"
	"time"
)

func (uc *BillingUsecase) SetVendorReconciliationRepo(r VendorReconciliationRepo) {
	uc.vendorReports = r
}
func (uc *BillingUsecase) vendorAuth(ctx context.Context, op string) (context.Context, error) {
	ctx, err := prepareBilling(ctx, uc.authorization, "billing.vendor_reports", op)
	if err != nil {
		return ctx, err
	}
	if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
		err = authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()})
	}
	return ctx, err
}
func (uc *BillingUsecase) ImportVendorReport(ctx context.Context, m VendorManifest, rows []VendorBillRow, expected, reason string, attrs map[string][]VendorAttribution) (VendorReconciliation, error) {
	var out VendorReconciliation
	ctx, err := uc.vendorAuth(ctx, "billing.vendor_report.import")
	if err != nil {
		return out, err
	}
	if uc.vendorReports == nil {
		return out, fmt.Errorf("vendor report persistence unavailable")
	}
	if reason == "" || expected != VendorReportID(m, rows) {
		return out, fmt.Errorf("fixed report identity and write reason required")
	}
	m.Reason = reason
	m.Actor = "authenticated-legacy-operator"
	if q, ok := authorization.QueryScopeFromContext(ctx, "billing.vendor_report.import"); ok {
		m.Actor = "iam-user:" + strconv.FormatInt(q.ActorID, 10)
	}
	ctx = authorization.WithWriteReason(ctx, reason)
	if len(attrs) > 0 {
		ctx, err = uc.vendorAuth(ctx, "billing.vendor_report.attribute")
		if err != nil {
			return out, err
		}
	}
	local, err := uc.vendorReports.LoadVendorAttempts(ctx, m)
	if err != nil {
		return out, err
	}
	out, err = ReconcileVendor(m, rows, local)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for i := range out.Comparisons {
		c := &out.Comparisons[i]
		items, ok := attrs[c.Key]
		if !ok {
			continue
		}
		for _, a := range items {
			if seen[a.EvidenceID] || a.Scope != m.Scope.Granularity {
				return out, fmt.Errorf("duplicate evidence or incomparable attribution scope")
			}
			seen[a.EvidenceID] = true
		}
		if err := AttributeVendorDifference(c, items); err != nil {
			return out, err
		}
		delete(attrs, c.Key)
	}
	if len(attrs) > 0 {
		return out, fmt.Errorf("unknown comparison key")
	}
	return out, uc.vendorReports.StoreVendorReconciliation(ctx, &out)
}
func (uc *BillingUsecase) ListVendorReports(ctx context.Context, start, end time.Time, limit int) ([]VendorReconciliation, []VendorDailySummary, error) {
	ctx, err := uc.vendorAuth(ctx, "billing.vendor_report.read")
	if err != nil {
		return nil, nil, err
	}
	if uc.vendorReports == nil || !start.Before(end) || end.Sub(start) > 31*24*time.Hour || limit < 1 || limit > 100 {
		return nil, nil, fmt.Errorf("bounded fixed reporting window required")
	}
	rows, err := uc.vendorReports.ListVendorReconciliations(ctx, start, end, limit)
	if err != nil {
		return nil, nil, err
	}
	return rows, SummarizeVendorDaily(rows), nil
}
func DecodeVendorJSON(raw []byte, value any) error {
	if len(raw) > 2<<20 {
		return fmt.Errorf("vendor document too large")
	}
	d := jsonx.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var next any
	if err := d.Decode(&next); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

type VendorDailySummary struct {
	Day              string `json:"day"`
	Provider         string `json:"provider"`
	Account          string `json:"account"`
	Currency         string `json:"currency"`
	Unit             string `json:"unit"`
	Reports          int    `json:"reports"`
	Pending          int    `json:"pending"`
	Attention        int    `json:"attention"`
	UnknownAmounts   int    `json:"unknown_amounts"`
	InternalAttempts int    `json:"internal_attempts"`
	LatestEvidenceID string `json:"latest_evidence_id"`
}
