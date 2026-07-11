package main

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/monitor/internal/biz"
	monitordata "micro-one-api/app/monitor/internal/data"
	monitorcfg "micro-one-api/app/monitor/internal/conf"
	applogger "micro-one-api/platform/logging"

	"go.uber.org/zap"
)

// newChannelHealthCheckerImpl creates a background channel health checker
// that polls the channel-service. It returns a no-op cleanup when health
// checking is disabled.
func newChannelHealthCheckerImpl(cfg *monitorcfg.Config) (*biz.ChannelHealthChecker, func()) {
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
