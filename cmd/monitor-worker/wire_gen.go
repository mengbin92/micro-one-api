//go:build !wireinject

package main

import (
	"fmt"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"

	monitorcfg "micro-one-api/internal/monitor/config"
	"micro-one-api/internal/pkg/xconfig"
	"micro-one-api/internal/monitor/biz"
	"micro-one-api/internal/monitor/data"
	"micro-one-api/internal/monitor/server"
	"micro-one-api/internal/monitor/service"
	appregistry "micro-one-api/internal/pkg/registry"
)

func loadConfig(confPath string) (*monitorcfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg monitorcfg.Config
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

	uc := biz.NewMonitorUsecase(repo)
	svc := service.NewMonitorService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		fmt.Printf("Warning: Failed to create registrar: %v\n", rErr)
	}

	kratosOpts := []kratos.Option{
		kratos.Name("monitor-worker"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}

	app := kratos.New(kratosOpts...)

	return app, func() {}, nil
}
