package main

import (
	"os"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"

	_ "github.com/go-kratos/kratos/v2/config/file"
)

func main() {
	confPath := os.Getenv("CONF_PATH")
	if confPath == "" {
		confPath = "configs/config.yaml"
	}

	applogger.InitializeStartupLogger()
	defer applogger.Sync()

	warnIfLogDeleteUnconfigured()

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
