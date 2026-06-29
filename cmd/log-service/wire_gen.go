//go:build !wireinject

package main

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"go.uber.org/zap"

	"micro-one-api/internal/log/biz"
	logcfg "micro-one-api/internal/log/config"
	"micro-one-api/internal/log/data"
	"micro-one-api/internal/log/server"
	"micro-one-api/internal/log/service"
	applogger "micro-one-api/internal/pkg/logger"
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"
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
	cleanupRetention := startLogRetentionCleanup(uc, cfg.Log.RetentionDays)

	// Partition maintenance for the `logs` table. Gated by the partition
	// feature flag (default off); a no-op when the repository is in-memory.
	// REVIEW_v4 §六 listed this as a remaining optional optimization item.
	partitionCtx, partitionCancel := context.WithCancel(context.Background())
	partitionStop := startPartitionMaintenance(partitionCtx, repo.DB(), cfg.Partition)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
	}

	kratosOpts := []kratos.Option{
		kratos.Name("log-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}

	app := kratos.New(kratosOpts...)

	return app, func() {
		cleanupRetention()
		partitionCancel()
		if partitionStop != nil {
			partitionStop()
		}
	}, nil
}
