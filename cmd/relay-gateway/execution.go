package main

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	billingv1 "micro-one-api/api/billing/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/domain/execution"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayservice "micro-one-api/internal/service"
	grpcauth "micro-one-api/platform/grpc"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/security/serviceidentity"
	"os"
	"sync"
)

func newExecutionDelivery(client billingv1.BillingServiceClient) (*relaybiz.ExecutionDelivery, func(), error) {

	path := os.Getenv("RELAY_EXECUTION_OUTBOX_PATH")
	if path == "" {
		path = "/var/lib/relay-outbox/events.db"
	}
	if os.Getenv("RELAY_EXECUTION_DELIVERY_V1") != "true" && os.Getenv("UPSTREAM_PROBES_ENABLED") != "true" {
		// #nosec G703 -- This path is trusted server configuration, not request input; retained outbox data must survive disabling the feature.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, func() {}, nil
		} else if err != nil {
			return nil, nil, err
		}
	}
	d, closeDB, err := relaydata.NewExecutionData(path, relayservice.NewExecutionSender(client))
	if err != nil {
		return nil, nil, fmt.Errorf("durable execution outbox: %w", err)
	}
	delivery := relaybiz.NewExecutionDelivery(relaydata.NewExecutionOutboxRepo(d, path))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		delivery.Run(ctx, func(err error) { applogger.Log.Warn("execution outbox delivery deferred", zap.Error(err)) })
	}()
	return delivery, func() { cancel(); wg.Wait(); closeDB() }, nil
}
func configuredUpstreamProbes() ([]execution.ProbeTarget, error) {
	if os.Getenv("UPSTREAM_PROBES_ENABLED") != "true" {
		return nil, nil
	}
	return execution.DecodeProbeTargets(os.Getenv("UPSTREAM_PROBE_TARGETS_JSON"))
}

func newProbePermitClient() (relaybiz.ProbePermitClient, func(), error) {
	endpoint := os.Getenv("MONITOR_GRPC_ENDPOINT")
	if endpoint == "" {
		return nil, nil, fmt.Errorf("monitor budget permit endpoint required")
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(serviceidentity.ClientToken())))
	if err != nil {
		return nil, nil, err
	}
	return relayservice.NewProbePermitClient(monitorv1.NewMonitorServiceClient(conn)), func() { _ = conn.Close() }, nil
}
