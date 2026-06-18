//go:build !wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	channelv1 "micro-one-api/api/channel/v1"

	"micro-one-api/internal/monitor/biz"
	monitorcfg "micro-one-api/internal/monitor/config"
	"micro-one-api/internal/monitor/data"
	"micro-one-api/internal/monitor/server"
	"micro-one-api/internal/monitor/service"
	applogger "micro-one-api/internal/pkg/logger"
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"
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
	_, channelCleanup := newChannelHealthChecker(cfg)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
	}

	kratosOpts := []kratos.Option{
		kratos.Name("monitor-worker"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}

	app := kratos.New(kratosOpts...)

	return app, func() {
		if channelCleanup != nil {
			channelCleanup()
		}
	}, nil
}

func newChannelHealthChecker(cfg *monitorcfg.Config) (*biz.ChannelHealthChecker, func()) {
	if cfg == nil || !cfg.Monitor.ChannelHealthCheckEnabled || cfg.Clients.Channel.Endpoint == "" {
		return nil, nil
	}
	interval := 5 * time.Minute
	if cfg.Monitor.ChannelHealthCheckInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Monitor.ChannelHealthCheckInterval); err == nil && parsed > 0 {
			interval = parsed
		}
	}
	timeout := 10 * time.Second
	if cfg.Monitor.ChannelHealthCheckTimeout != "" {
		if parsed, err := time.ParseDuration(cfg.Monitor.ChannelHealthCheckTimeout); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	conn, err := grpc.NewClient(cfg.Clients.Channel.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		applogger.Log.Warn("failed to create channel health client", zap.Error(err))
		return nil, nil
	}
	checker := biz.NewChannelHealthChecker(channelv1.NewChannelServiceClient(conn), biz.ChannelHealthCheckerConfig{
		Enabled:  true,
		Interval: interval,
		Timeout:  timeout,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go checker.Run(ctx)
	return checker, func() {
		cancel()
		_ = conn.Close()
	}
}
