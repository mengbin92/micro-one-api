//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"

	"micro-one-api/app/notify/internal/biz"
	notifycfg "micro-one-api/app/notify/internal/conf"
	"micro-one-api/app/notify/internal/data"
	"micro-one-api/app/notify/internal/server"
	"micro-one-api/app/notify/internal/service"

	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newRepo,
	biz.NewNotifyUsecase,
	service.NewNotifyService,
	server.NewGRPCServer,
	server.NewHTTPServer,
	provideRegistrar,
	wire.Bind(new(biz.NotifyRepo), new(*data.Repository)),
)

func newRepo(cfg *notifycfg.Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
}

type registrarResult struct {
	Registrar registry.Registrar
}

func provideRegistrar(cfg *notifycfg.Config) registrarResult {
	registrar, err := appregistry.NewRegistrar(cfg.Registry)
	if err != nil {
		return registrarResult{}
	}
	return registrarResult{Registrar: registrar}
}

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		newApp,
	))
}

func newApp(cfg *notifycfg.Config, uc *biz.NotifyUsecase, svc *service.NotifyService, reg registrarResult) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	dispatchInterval, err := time.ParseDuration(cfg.Notify.DispatchInterval)
	if err != nil {
		dispatchInterval = 30 * time.Second
	}
	sender := biz.NewMultiSender(biz.SenderConfig{
		WebhookURL:         cfg.Notify.WebhookURL,
		SMTPHost:           cfg.Notify.SMTPHost,
		SMTPPort:           cfg.Notify.SMTPPort,
		SMTPUser:           cfg.Notify.SMTPUser,
		SMTPPass:           cfg.Notify.SMTPPass,
		SMTPFrom:           cfg.Notify.SMTPFrom,
		WeComWebhookURL:    cfg.Notify.WeComWebhookURL,
		DingTalkWebhookURL: cfg.Notify.DingTalkWebhookURL,
		FeishuWebhookURL:   cfg.Notify.FeishuWebhookURL,
		SlackWebhookURL:    cfg.Notify.SlackWebhookURL,
	})
	dispatcher := biz.NewDispatcher(uc, sender, dispatchInterval, cfg.Notify.DispatchBatch, cfg.Notify.MaxRetry)
	stopDispatcher := dispatcher.Start(context.Background())
	opts := []kratos.Option{
		kratos.Name("notify-worker"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)
	return app, stopDispatcher
}
