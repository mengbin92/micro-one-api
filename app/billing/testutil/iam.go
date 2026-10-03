package testutil

import (
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/app/billing/internal/server"
	"micro-one-api/app/billing/internal/service"
	"micro-one-api/platform/authz"
)

func NewIAMStack(db *gorm.DB, resolver *authz.Client) *kgrpc.Server {
	d := data.NewOwnerDataWithDB(db)
	uc := biz.NewBillingUsecase(d.AccountRepo(), d.ReservationRepo(), d.LedgerRepo(), d.RedeemRepo(), nil)
	uc.SetTxRunner(data.NewTxRunner(d))
	uc.SetAuthorization(resolver)
	payment := biz.NewPaymentUsecase(d.PaymentRepo(), nil, nil)
	payment.SetAuthorization(resolver)
	svc := service.NewBillingService(uc, nil, payment, nil)
	svc.SetOwnerAuthorization(resolver)
	return server.NewGRPCServer(":0", svc)
}
