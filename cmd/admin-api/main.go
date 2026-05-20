package main

import (
	"os"

	"github.com/go-kratos/kratos/v2/log"

	_ "github.com/go-kratos/kratos/v2/config/file"
)

func main() {
	confPath := os.Getenv("CONF_PATH")
	if confPath == "" {
		confPath = "configs/admin-api.yaml"
	}

	logger := log.NewStdLogger(os.Stdout)
	helper := log.NewHelper(logger)

	warnIfLogDeleteUnconfigured(helper)

	app, cleanup, err := InitApp(confPath)
	if err != nil {
		helper.Errorf("failed to create app: %v", err)
		os.Exit(1)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		helper.Errorf("failed to run app: %v", err)
		os.Exit(1)
	}
}
