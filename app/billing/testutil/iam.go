package testutil

import (
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/app/billing/internal/server"
	"micro-one-api/app/billing/internal/service"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
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
	subscriptions := subscriptiondata.NewRepository(db, nil)
	refund := biz.NewRefundUsecase(d.PaymentRepo(), d.AccountRepo(), d.LedgerRepo(), subscriptionbiz.NewSubscriptionUsecase(subscriptions, subscriptions))
	refund.SetAuthorization(resolver)
	svc.SetRefundUsecase(refund)
	return server.NewGRPCServer(":0", svc)
}
