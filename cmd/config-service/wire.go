//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"

	"micro-one-api/internal/config/biz"
	configcfg "micro-one-api/internal/config/config"
	"micro-one-api/internal/config/data"
	"micro-one-api/internal/config/server"
	"micro-one-api/internal/config/service"
)

var ProviderSet = wire.NewSet(
	data.NewRepositoryFromEnv,
	biz.NewConfigUsecase,
	service.NewConfigService,
	server.NewGRPCServer,
)

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		newApp,
	))
}

func newApp(cfg *configcfg.Config, svc *service.ConfigService) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, cfg.Server.GRPC.Addr, svc)
	app := kratos.New(
		kratos.Name("config-service"),
		kratos.Server(grpcSrv, httpSrv),
	)
	return app, func() {}
}
