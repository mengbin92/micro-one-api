//go:build !wireinject

package main

import (
	"fmt"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"

	logcfg "micro-one-api/internal/log/config"
	"micro-one-api/internal/pkg/xconfig"
	"micro-one-api/internal/log/biz"
	"micro-one-api/internal/log/data"
	"micro-one-api/internal/log/server"
	"micro-one-api/internal/log/service"
	appregistry "micro-one-api/internal/pkg/registry"
)

func loadConfig(confPath string) (*logcfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg logcfg.Config
	if err := kratosCfg.Scan(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// InitApp loads config and builds the Kratos application.
func InitApp(confPath string) (*kratos.App, func(), error) {
	cfg, err := loadConfig(confPath)
	if err != nil {
		return nil, nil, err
	}

	repo, err := data.NewRepositoryFromEnv(cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	uc := biz.NewLogUsecase(repo)
	svc := service.NewLogService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		fmt.Printf("Warning: Failed to create registrar: %v\n", rErr)
	}

	kratosOpts := []kratos.Option{
		kratos.Name("log-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}

	app := kratos.New(kratosOpts...)

	return app, func() {}, nil
}
