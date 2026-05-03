//go:build !wireinject

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"

	"micro-one-api/internal/billing/biz"
	bcfg "micro-one-api/internal/billing/config"
	"micro-one-api/internal/billing/data"
	"micro-one-api/internal/billing/server"
	"micro-one-api/internal/billing/service"
)

func loadConfig(confPath string) (*bcfg.Config, error) {
	source := file.NewSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg bcfg.Config
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

	d, err := data.NewData(cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	groupRatios := cfg.Billing.GroupRatios
	if len(groupRatios) == 0 {
		groupRatios = biz.DefaultGroupRatios()
	}
	uc := biz.NewBillingUsecase(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.LedgerRepo(),
		d.RedeemRepo(),
		groupRatios,
	)
	svc := service.NewBillingService(uc)

	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)

	app := kratos.New(
		kratos.Name("billing-service"),
		kratos.Server(grpcSrv, httpSrv),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cleanupJob := biz.NewCleanupJob(uc, 1*time.Minute)
	go cleanupJob.Start(ctx)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		cancel()
		cleanupJob.Stop()
	}()

	return app, func() { d.Close(); cancel() }, nil
}
