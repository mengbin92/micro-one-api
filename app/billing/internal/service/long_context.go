package service

import "micro-one-api/app/billing/internal/biz"

// The ledger detail exposes customer tariff evidence, while procurement prices
// require a separate permission. Copy nested rules before removing procurement
// so serialization cannot mutate the reservation used by subsequent settlement.
func ledgerRequestSnapshotView(snapshot *biz.RequestSnapshot) *biz.RequestSnapshot {
	if snapshot == nil {
		return nil
	}
	view := *snapshot
	if view.ServiceTier != nil {
		tier := *view.ServiceTier
		tier.Config.UpstreamPrices = nil
		if tier.Config.TimePricing != nil {
			timePricing := *tier.Config.TimePricing
			timePricing.PeakUpstreamPrices = nil
			timePricing.PeakLongContextUpstreamPrices = nil
			tier.Config.TimePricing = &timePricing
		}
		if tier.Config.LongContext != nil {
			longContext := *tier.Config.LongContext
			longContext.UpstreamPrices = nil
			tier.Config.LongContext = &longContext
		}
		view.ServiceTier = &tier
	}
	return &view
}
