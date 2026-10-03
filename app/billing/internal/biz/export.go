package biz

import (
	"context"
)

type costExportKey struct{}

func CostExportSelected(ctx context.Context) bool {
	v, _ := ctx.Value(costExportKey{}).(bool)
	return v
}
func (uc *BillingUsecase) ExportCostReport(ctx context.Context, f UsageFilter, includeCosts bool) ([]*UsageBucket, *UsageTotals, error) {
	var err error
	ctx, err = prepareBilling(ctx, uc.authorization, "billing.report.export", "billing.report.export")
	if err != nil {
		return nil, nil, err
	}
	if includeCosts {
		ctx, err = prepareBilling(ctx, uc.authorization, "billing.accounts.read", "billing.account.cost.read")
		if err != nil {
			return nil, nil, err
		}
		ctx = context.WithValue(ctx, costExportKey{}, true)
	}
	if !includeCosts {
		ctx, err = uc.prepareCost(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	return uc.ledgerRepo.AggregateUsage(ctx, f)
}
func (uc *BillingUsecase) ExportRedeemCodes(ctx context.Context) ([]*RedeemCode, error) {
	ctx, err := prepareBilling(ctx, uc.authorization, "billing.redemption.export", "billing.redemption.export")
	if err != nil {
		return nil, err
	}
	out := []*RedeemCode{}
	for page := int32(1); ; page++ {
		rows, total, err := uc.redeemRepo.ListRedeemCodes(ctx, page, 100)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if int64(len(out)) >= total || len(rows) == 0 {
			break
		}
	}
	return out, nil
}

// ExportLedgerEntries has its own grant and filters account facts before count/paging.
func (uc *BillingUsecase) ExportLedgerEntries(ctx context.Context, options LedgerListOptions) ([]*Ledger, error) {
	ctx, err := prepareBilling(ctx, uc.authorization, "billing.report.export", "billing.report.export")
	if err != nil {
		return nil, err
	}
	ctx, err = uc.prepareCost(ctx)
	if err != nil {
		return nil, err
	}
	if options.Page <= 0 {
		options.Page = 1
	}
	if options.PageSize <= 0 || options.PageSize > 1000 {
		options.PageSize = 1000
	}
	var rows []*Ledger
	if repo, ok := uc.ledgerRepo.(OrderedLedgerRepo); ok {
		rows, _, err = repo.ListLedgersWithOptions(ctx, options)
	} else {
		rows, _, err = uc.ledgerRepo.ListLedgersWithFilters(ctx, options.UserID, options.Page, options.PageSize, options.Type, options.StartTime, options.EndTime)
	}
	return ledgerViews(ctx, rows), err
}
