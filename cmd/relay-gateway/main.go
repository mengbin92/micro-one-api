package main

import (
	"os"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"
	xtrace "micro-one-api/platform/tracing"
)

func main() {
	confPath := os.Getenv("CONF_PATH")
	if confPath == "" {
		confPath = "configs/config.yaml"
	}

	applogger.InitializeStartupLogger()
	defer applogger.Sync()
	stopTracing, err := xtrace.InitTracer(xtrace.Config{
		Enabled:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "",
		Endpoint:   os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		Service:    "relay-gateway",
		SampleRate: 1,
	})
	if err != nil {
		applogger.Log.Error("failed to initialize tracing", zap.Error(err))
		os.Exit(1)
	}
	defer stopTracing()
	if os.Getenv("PROVIDER_DISABLE_SSRF_CHECK") == "true" {
		applogger.Log.Warn("upstream SSRF protection is disabled", zap.String("setting", "PROVIDER_DISABLE_SSRF_CHECK"))
	}

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
