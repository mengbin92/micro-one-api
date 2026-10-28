package testutil

import (
	"gorm.io/gorm"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/app/billing/internal/service"
)

// NewServiceTierBilling borrows migrated scratch storage for acceptance tests.
// Pricing, reservations, snapshots and settlement use the production owners.
func NewServiceTierBilling(db *gorm.DB) billingv1.BillingServiceServer {
	d := data.NewOwnerDataWithDB(db)
	uc := biz.NewBillingUsecaseWithPricing(d.AccountRepo(), d.ReservationRepo(), d.LedgerRepo(), d.RedeemRepo(), biz.PricingConfig{
		PricingStore: data.NewPricingConfigRepo(d),
	})
	uc.SetTxRunner(data.NewTxRunner(d))
	uc.SetPricingSnapshotRepo(data.NewPricingSnapshotRepo(d))
	return service.NewBillingService(uc, nil, nil, nil)
}
