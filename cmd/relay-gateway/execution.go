package main

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	billingv1 "micro-one-api/api/billing/v1"
	logv1 "micro-one-api/api/log/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/domain/execution"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayservice "micro-one-api/internal/service"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/grpc/xgrpc"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
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

// newObservationDelivery opens the independent observation-only outbox. New
// collection and draining retained evidence are separate decisions: turning the
// producer off must still open the retained database and finish deliveries, or
// a rollback would strand every pending event.
func newObservationDelivery(client logv1.LogServiceClient) (*relaybiz.ObservationDelivery, func(), error) {
	enabled := relaybiz.ObservationEnabled()
	path := os.Getenv("RELAY_OBSERVATION_OUTBOX_PATH")
	if path == "" {
		path = "/var/lib/relay-observation/observations.db"
	}
	scope, err := relaybiz.NewObservationScope(os.Getenv("RELAY_OBSERVATION_SCOPE"))
	if err != nil {
		return nil, nil, fmt.Errorf("observation scope: %w", err)
	}
	// Expected is configuration, not the producer switch: alert rules use it to
	// separate "legitimately disabled" from "this target lost observation".
	if os.Getenv("RELAY_USAGE_OBSERVATION_EXPECTED") == "true" {
		metrics.UsageObservationExpected.Set(1)
	}
	if !enabled {
		// #nosec G703 -- trusted server configuration; retained evidence must
		// survive disabling the feature.
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return nil, func() {}, nil
		} else if statErr != nil {
			return nil, nil, statErr
		}
	}
	sender := relayservice.NewUsageObservationSender(client)
	d, closeDB, err := relaydata.NewObservationData(path, sender)
	if err != nil {
		return nil, nil, fmt.Errorf("observation outbox: %w", err)
	}
	repo, err := relaydata.NewObservationOutboxRepo(d, path)
	if err != nil {
		closeDB()
		return nil, nil, err
	}
	delivery := relaybiz.NewObservationDelivery(repo, sender)
	delivery.SetScope(scope)
	if enabled {
		metrics.UsageObservationEnabled.Set(1)
	}
	metrics.UsageObservationReady.Set(1)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		delivery.Run(ctx, func(err error) { applogger.Log.Warn("usage observation delivery deferred", zap.Error(err)) })
	}()
	return delivery, func() {
		cancel()
		wg.Wait()
		metrics.UsageObservationReady.Set(0)
		metrics.UsageObservationEnabled.Set(0)
		closeDB()
	}, nil
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
	conn, err := grpc.NewClient(endpoint, grpc.WithStatsHandler(xgrpc.ClientStatsHandler()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(serviceidentity.ClientToken())))
	if err != nil {
		return nil, nil, err
	}
	return relayservice.NewProbePermitClient(monitorv1.NewMonitorServiceClient(conn)), func() { _ = conn.Close() }, nil
}
