//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	channelv1 "micro-one-api/api/channel/v1"

	"micro-one-api/internal/monitor/biz"
	monitorcfg "micro-one-api/internal/monitor/config"
	"micro-one-api/internal/monitor/data"
	"micro-one-api/internal/monitor/server"
	"micro-one-api/internal/monitor/service"
)

var ProviderSet = wire.NewSet(
	data.NewRepositoryFromEnv,
	biz.NewMonitorUsecase,
	service.NewMonitorService,
	server.NewGRPCServer,
)

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		newApp,
	))
}

func newApp(cfg *monitorcfg.Config, svc *service.MonitorService) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	_, channelCleanup := newChannelHealthChecker(cfg)
	app := kratos.New(
		kratos.Name("monitor-worker"),
		kratos.Server(grpcSrv, httpSrv),
	)
	return app, func() {
		if channelCleanup != nil {
			channelCleanup()
		}
	}
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
		fmt.Printf("Warning: Failed to create channel health client: %v\n", err)
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
