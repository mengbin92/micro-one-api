//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"

	"micro-one-api/internal/notify/biz"
	notifycfg "micro-one-api/internal/notify/config"
	"micro-one-api/internal/notify/data"
	"micro-one-api/internal/notify/server"
	"micro-one-api/internal/notify/service"
)

var ProviderSet = wire.NewSet(
	data.NewRepositoryFromEnv,
	biz.NewNotifyUsecase,
	service.NewNotifyService,
	server.NewGRPCServer,
)

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		newApp,
	))
}

func newApp(cfg *notifycfg.Config, uc *biz.NotifyUsecase, svc *service.NotifyService) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, cfg.Server.GRPC.Addr, svc)
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
	app := kratos.New(
		kratos.Name("notify-worker"),
		kratos.Server(grpcSrv, httpSrv),
	)
	return app, stopDispatcher
}
