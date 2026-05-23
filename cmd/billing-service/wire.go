//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"

	"micro-one-api/internal/billing/biz"
	bcfg "micro-one-api/internal/billing/config"
	"micro-one-api/internal/billing/data"
	"micro-one-api/internal/billing/server"
	"micro-one-api/internal/billing/service"
)

var ProviderSet = wire.NewSet(
	data.NewData,
	biz.NewBillingUsecase,
	biz.NewPaymentAssetIssuer,
	biz.NewMockPaymentProvider,
	biz.NewConfiguredPaymentProvider,
	biz.NewAlipayPaymentProvider,
	biz.NewPaymentUsecase,
	biz.NewReconciliationUsecase,
	service.NewBillingService,
	server.NewGRPCServer,
	server.NewHTTPServer,
)

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		newApp,
	))
}

func newApp(cfg *bcfg.Config, svc *service.BillingService) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	app := kratos.New(
		kratos.Name("billing-service"),
		kratos.Server(grpcSrv, httpSrv),
	)
	return app, func() {}
}
