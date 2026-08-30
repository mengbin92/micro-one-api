package main

import (
	"context"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/monitor/internal/biz"
	monitordata "micro-one-api/app/monitor/internal/data"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/grpc/xgrpc"
	applogger "micro-one-api/platform/logging"

	"go.uber.org/zap"
)

// newChannelHealthCheckerImpl creates a background channel health checker
// that polls the channel-service. It returns a no-op cleanup when health
// checking is disabled.
func newChannelHealthCheckerImpl(cfg *Config) (*biz.ChannelHealthChecker, func()) {
	if cfg == nil || cfg.Bootstrap == nil || cfg.Bootstrap.MonitorSvc == nil {
		return nil, nil
	}
	monitorSvc := cfg.Bootstrap.MonitorSvc
	clients := cfg.Bootstrap.Clients

	if !monitorSvc.ChannelHealthCheckEnabled || clients == nil || clients.Channel == nil || clients.Channel.Endpoint == "" {
		return nil, nil
	}

	// Parse interval with fallback to 5m.
	interval := 5 * time.Minute
	if monitorSvc.ChannelHealthCheckInterval != nil {
		if d := monitorSvc.ChannelHealthCheckInterval.AsDuration(); d > 0 {
			interval = d
		}
	}

	// Parse timeout with fallback to 10s.
	timeout := 10 * time.Second
	if monitorSvc.ChannelHealthCheckTimeout != nil {
		if d := monitorSvc.ChannelHealthCheckTimeout.AsDuration(); d > 0 {
			timeout = d
		}
	}

	conn, err := grpc.NewClient(clients.Channel.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(os.Getenv("SERVICE_TOKEN"))),
		grpc.WithChainUnaryInterceptor(xgrpc.UnaryClientMetricsInterceptor("channel-service")))
	if err != nil {
		applogger.Log.Warn("failed to create channel health client", zap.Error(err))
		return nil, nil
	}
	adapter := monitordata.NewChannelProbeAdapter(channelv1.NewChannelServiceClient(conn))
	checker := biz.NewChannelHealthChecker(adapter, biz.ChannelHealthCheckerConfig{
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
