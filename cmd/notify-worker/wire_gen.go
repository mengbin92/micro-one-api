//go:build !wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"go.uber.org/zap"

	"micro-one-api/internal/notify/biz"
	notifycfg "micro-one-api/internal/notify/config"
	"micro-one-api/internal/notify/data"
	"micro-one-api/internal/notify/server"
	"micro-one-api/internal/notify/service"
	applogger "micro-one-api/internal/pkg/logger"
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"
)

func loadConfig(confPath string) (*notifycfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg notifycfg.Config
	if err := kratosCfg.Scan(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// InitApp loads config and builds the Kratos application.
func InitApp(confPath string) (*kratos.App, func(), error) {
	cfg, err := loadConfig(confPath)
	if err != nil {
		return nil, nil, err
	}

	repo, err := data.NewRepositoryFromEnv(cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	uc := biz.NewNotifyUsecase(repo)
	svc := service.NewNotifyService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	dispatchInterval, err := time.ParseDuration(cfg.Notify.DispatchInterval)
	if err != nil {
		dispatchInterval = 30 * time.Second
	}
	sender := biz.NewMultiSender(biz.SenderConfig{
		WebhookURL: cfg.Notify.WebhookURL,
		SMTPHost:   cfg.Notify.SMTPHost,
		SMTPPort:   cfg.Notify.SMTPPort,
		SMTPUser:   cfg.Notify.SMTPUser,
		SMTPPass:   cfg.Notify.SMTPPass,
		SMTPFrom:   cfg.Notify.SMTPFrom,
	})
	dispatcher := biz.NewDispatcher(uc, sender, dispatchInterval, cfg.Notify.DispatchBatch, cfg.Notify.MaxRetry)
	stopDispatcher := dispatcher.Start(context.Background())

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
	}

	kratosOpts := []kratos.Option{
		kratos.Name("notify-worker"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}

	app := kratos.New(kratosOpts...)

	return app, stopDispatcher, nil
}
