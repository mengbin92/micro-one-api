package main

import (
	xtrace "micro-one-api/platform/tracing"
	"os"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"
)

func main() {
	confPath := os.Getenv("CONF_PATH")
	if confPath == "" {
		confPath = "configs/config.yaml"
	}

	applogger.InitializeStartupLogger()
	defer applogger.Sync()
	if envBool("SUBSCRIPTION_ACCOUNT_OPS_WORKER_ONLY", false) {
		if _, err := accountOpsShardFromEnv(); err != nil {
			applogger.Log.Error("invalid account ops worker shard", zap.Error(err))
			os.Exit(1)
		}
		if !envBool("SUBSCRIPTION_QUOTA_RESET_ENABLED", false) && !envBool("SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED", false) {
			applogger.Log.Error("account ops worker requires an enabled sweeper")
			os.Exit(1)
		}
	}

	stopTracing, err := xtrace.InitTracer(xtrace.Config{Enabled: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "", Endpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), Service: "channel-service", SampleRate: 1})
	if err != nil {
		applogger.Log.Error("failed to initialize tracing", zap.Error(err))
		os.Exit(1)
	}
	defer stopTracing()
	app, cleanup, err := InitApp(confPath)
	if err != nil {
		applogger.Log.Error("failed to create app", zap.Error(err))
		os.Exit(1)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		applogger.Log.Error("failed to run app", zap.Error(err))
		os.Exit(1)
	}
}
