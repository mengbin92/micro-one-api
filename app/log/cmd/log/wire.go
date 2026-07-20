//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/registry"
	"github.com/google/wire"

	"micro-one-api/app/log/internal/biz"
	"micro-one-api/app/log/internal/data"
	"micro-one-api/app/log/internal/server"
	"micro-one-api/app/log/internal/service"

	applogger "micro-one-api/platform/logging"
	appregistry "micro-one-api/platform/registry"

	"go.uber.org/zap"
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

func newRepo(cfg *Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Bootstrap.Data.Database.Driver, cfg.Bootstrap.Data.Database.Source, cfg.Bootstrap.Data.Database.Schema)
}

type registrarResult struct {
	Registrar registry.Registrar
}

func provideRegistrar(cfg *Config) registrarResult {
	registrar, err := appregistry.NewRegistrar(cfg.Registry())
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

func newApp(cfg *Config, repo *data.Repository, uc *biz.LogUsecase, svc *service.LogService, reg registrarResult) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Bootstrap.Server.Grpc.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Bootstrap.Server.Http.Addr, svc)

	// Parse retention days with fallback to 30.
	retentionDays := 30
	if cfg.Bootstrap.LogSvc != nil && cfg.Bootstrap.LogSvc.RetentionDays > 0 {
		retentionDays = int(cfg.Bootstrap.LogSvc.RetentionDays)
	}
	cleanupRetention := startLogRetentionCleanup(uc, retentionDays)

	partitionCtx, partitionCancel := context.WithCancel(context.Background())
	partitionStop := startPartitionMaintenance(partitionCtx, repo.DB(), cfg.Bootstrap.Partition)

	// Phase 2.3: optional async batch log writer. When disabled (default),
	// LogUsecase.IngestLog falls back to synchronous repo.Create; when
	// enabled, entries are queued and flushed in batches via
	// Repository.CreateBatch (gorm CreateInBatches), lowering per-request
	// log latency. The writer's lifecycle is tied to the app cleanup.
	var batchWriter *biz.BatchLogWriter
	var batchStop func()
	if cfg.Bootstrap.LogSvc != nil && cfg.Bootstrap.LogSvc.BatchEnabled {
		batchSize := int(cfg.Bootstrap.LogSvc.BatchSize)
		if batchSize <= 0 {
			batchSize = 100
		}

		// Parse batch flush interval with fallback to 1s.
		flushInterval := time.Second
		if cfg.Bootstrap.LogSvc.BatchFlushInterval != nil {
			if d := cfg.Bootstrap.LogSvc.BatchFlushInterval.AsDuration(); d > 0 {
				flushInterval = d
			}
		}

		batchWriter = biz.NewBatchLogWriter(repo, batchSize, flushInterval)
		batchWriter.Start()
		uc.SetBatchWriter(batchWriter)
		batchStop = batchWriter.Stop
		if applogger.Log != nil {
			applogger.Log.Info("log batch writer enabled",
				zap.Int("batch_size", batchSize),
				zap.Duration("flush_interval", flushInterval))
		}
	}

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
		if batchStop != nil {
			batchStop()
		}
	}
}
