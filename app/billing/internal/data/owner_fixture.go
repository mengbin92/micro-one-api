package data

import "gorm.io/gorm"

// NewOwnerDataWithDB borrows a caller-owned scratch database for cross-service
// acceptance tests; it does not create or own another storage client.
func NewOwnerDataWithDB(db *gorm.DB) *Data {
	d := &Data{db: db}
	d.accountRepo = NewAccountRepo(d)
	d.reservationRepo = NewReservationRepo(d)
	d.ledgerRepo = NewLedgerRepo(d)
	d.redeemRepo = NewRedeemRepo(d)
	d.paymentRepo = NewPaymentRepo(d)
	return d
}
