//go:build wireinject
// +build wireinject

package main

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"

	"micro-one-api/app/log/internal/biz"
	logcfg "micro-one-api/app/log/internal/conf"
	"micro-one-api/app/log/internal/data"
	"micro-one-api/app/log/internal/server"
	"micro-one-api/app/log/internal/service"

	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newRepo,
	biz.NewLogUsecase,
	service.NewLogService,
	server.NewGRPCServer,
	server.NewHTTPServer,
	provideRegistrar,
	wire.Bind(new(biz.LogRepo), new(*data.Repository)),
)

func newRepo(cfg *logcfg.Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
}

type registrarResult struct {
	Registrar registry.Registrar
}

func provideRegistrar(cfg *logcfg.Config) registrarResult {
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

func newApp(cfg *logcfg.Config, repo *data.Repository, uc *biz.LogUsecase, svc *service.LogService, reg registrarResult) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	cleanupRetention := startLogRetentionCleanup(uc, cfg.Log.RetentionDays)
	partitionCtx, partitionCancel := context.WithCancel(context.Background())
	partitionStop := startPartitionMaintenance(partitionCtx, repo.DB(), cfg.Partition)
	opts := []kratos.Option{
		kratos.Name("log-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)
	return app, func() {
		cleanupRetention()
		partitionCancel()
		if partitionStop != nil {
			partitionStop()
		}
	}
}
