package testutil

import (
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"gorm.io/gorm"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/app/billing/internal/server"
	"micro-one-api/app/billing/internal/service"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/routingclient"
)

func NewIAMStack(db *gorm.DB, resolver *authz.Client) *kgrpc.Server {
	return server.NewGRPCServer(":0", newIAMService(db, resolver, nil, nil))
}

type AlipayConfig = biz.AlipayConfig

// NewIAMAlipayStack uses the same service for owner RPCs and signed HTTP
// callbacks, with caller-owned scratch storage and temporary test keys.
func NewIAMAlipayStack(db *gorm.DB, resolver *authz.Client, config AlipayConfig, channel channelv1.ChannelServiceClient) (*kgrpc.Server, *khttp.Server) {
	svc := newIAMService(db, resolver, &config, channel)
	return server.NewGRPCServer(":0", svc), server.NewHTTPServer(":0", svc)
}

func newIAMService(db *gorm.DB, resolver *authz.Client, config *AlipayConfig, channel channelv1.ChannelServiceClient) *service.BillingService {
	d := data.NewOwnerDataWithDB(db)
	uc := biz.NewBillingUsecase(d.AccountRepo(), d.ReservationRepo(), d.LedgerRepo(), d.RedeemRepo(), nil)
	uc.SetTxRunner(data.NewTxRunner(d))
	uc.SetAuthorization(resolver)
	subscriptions := subscriptiondata.NewRepository(db, nil)
	subscriptionUc := subscriptionbiz.NewSubscriptionUsecase(subscriptions, subscriptions)
	if channel != nil {
		uc.SetRoutingGroupReader(routingclient.New(channel))
		uc.SetRoutingPolicyRepo(data.NewRoutingPolicyRepo(d))
		subscriptionUc.SetContractGroupReader(uc)
	}
	var provider biz.PaymentProvider = biz.NewMockPaymentProvider()
	var verifier biz.PaymentNotifyVerifier
	if config != nil {
		alipay := biz.NewAlipayPaymentProvider(*config)
		provider, verifier = alipay, alipay
	}
	payment := biz.NewPaymentUsecaseWithAssignerAndSnapshotter(d.PaymentRepo(), provider, nil, biz.NewPaymentSubscriptionAssigner(subscriptionUc, subscriptions, subscriptions), biz.NewPaymentPlanSnapshotter(subscriptions, uc))
	payment.SetSubscriptionPurchaseValidator(subscriptionUc)
	payment.SetAuthorization(resolver)
	svc := service.NewBillingService(uc, nil, payment, verifier)
	if config != nil {
		svc.SetExpectedAlipayAppID(config.AppID)
	}
	svc.SetOwnerAuthorization(resolver)
	svc.SetSubscriptionCommerce(biz.NewSubscriptionCommerce(uc, subscriptionUc, subscriptions, data.NewSubscriptionCommerceRepo(d)))
	uc.SetSubscriptionPrimatives(subscriptionbiz.NewSubscriptionUsecase(subscriptions, subscriptions))
	refund := biz.NewRefundUsecase(d.PaymentRepo(), d.AccountRepo(), d.LedgerRepo(), subscriptionbiz.NewSubscriptionUsecase(subscriptions, subscriptions))
	refund.SetAuthorization(resolver)
	svc.SetRefundUsecase(refund)
	return svc
}
