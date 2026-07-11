//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"

	"micro-one-api/app/monitor/internal/biz"
	monitorcfg "micro-one-api/app/monitor/internal/conf"
	"micro-one-api/app/monitor/internal/data"
	"micro-one-api/app/monitor/internal/server"
	"micro-one-api/app/monitor/internal/service"

	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newRepo,
	biz.NewMonitorUsecase,
	service.NewMonitorService,
	server.NewGRPCServer,
	server.NewHTTPServer,
	provideRegistrar,
	wire.Bind(new(biz.MonitorRepo), new(*data.Repository)),
)

func newRepo(cfg *monitorcfg.Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
}

type registrarResult struct {
	Registrar registry.Registrar
}

func provideRegistrar(cfg *monitorcfg.Config) registrarResult {
	registrar, err := appregistry.NewRegistrar(cfg.Registry)
	if err != nil {
		return registrarResult{}
	}
	return registrarResult{Registrar: registrar}
}

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		newApp,
	))
}

func newApp(cfg *monitorcfg.Config, svc *service.MonitorService, reg registrarResult) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	_, channelCleanup := newChannelHealthChecker(cfg)
	opts := []kratos.Option{
		kratos.Name("monitor-worker"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)
	return app, func() {
		if channelCleanup != nil {
			channelCleanup()
		}
	}
}

func newChannelHealthChecker(cfg *monitorcfg.Config) (*biz.ChannelHealthChecker, func()) {
	return newChannelHealthCheckerImpl(cfg)
}
