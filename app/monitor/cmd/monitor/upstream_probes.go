package main

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	relayv1 "micro-one-api/api/relay/v1"
	"micro-one-api/app/monitor/internal/biz"
	"micro-one-api/app/monitor/internal/data"
	"micro-one-api/app/monitor/internal/service"
	"micro-one-api/domain/execution"
	grpcauth "micro-one-api/platform/grpc"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/security/serviceidentity"
	"os"
	"sync"
	"time"
)

func startUpstreamProbes(repo *data.Repository) (func(), error) {
	if os.Getenv("UPSTREAM_PROBES_ENABLED") != "true" {
		return func() {}, nil
	}
	if !repo.ProbeStorageReady(context.Background()) {
		return nil, fmt.Errorf("persistent probe budget/report/coverage storage required")
	}
	targets, err := execution.DecodeProbeTargets(os.Getenv("UPSTREAM_PROBE_TARGETS_JSON"))
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("enabled probes require explicit targets and budgets")
	}
	interval := 5 * time.Minute
	if raw := os.Getenv("UPSTREAM_PROBE_INTERVAL"); raw != "" {
		interval, err = time.ParseDuration(raw)
		if err != nil {
			return nil, err
		}
	}
	endpoint := os.Getenv("RELAY_GRPC_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("probe relay endpoint required")
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(serviceidentity.ClientToken())))
	if err != nil {
		return nil, err
	}
	scheduler, err := biz.NewUpstreamProbeScheduler(repo, service.NewUpstreamProbeClient(relayv1.NewRelayServiceClient(conn)), targets, interval)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scheduler.Run(ctx, func(err error) { applogger.Log.Warn("upstream probe failed", zap.Error(err)) })
	}()
	return func() { cancel(); wg.Wait(); _ = conn.Close() }, nil
}
