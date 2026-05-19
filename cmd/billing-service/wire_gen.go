//go:build !wireinject

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"

	"micro-one-api/internal/billing/biz"
	"micro-one-api/internal/pkg/xconfig"
	bcfg "micro-one-api/internal/billing/config"
	"micro-one-api/internal/billing/data"
	"micro-one-api/internal/billing/server"
	"micro-one-api/internal/billing/service"
	appregistry "micro-one-api/internal/pkg/registry"
)

func loadConfig(confPath string) (*bcfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
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
	reconUc := biz.NewReconciliationUsecase(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.ReconciliationRepo(),
		d.ReconciliationRunStore(),
	)
	svc := service.NewBillingService(uc, reconUc)

	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		fmt.Printf("Warning: Failed to create registrar: %v\n", rErr)
	}

	kratosOpts := []kratos.Option{
		kratos.Name("billing-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	ctx, cancel := context.WithCancel(context.Background())
	cleanupJob := biz.NewCleanupJob(uc, 1*time.Minute)
	reconJob := biz.NewReconciliationJob(reconUc, 1*time.Hour)
	go cleanupJob.Start(ctx)
	go reconJob.Start(ctx)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		cancel()
		cleanupJob.Stop()
		reconJob.Stop()
	}()

	return app, func() { d.Close(); cancel() }, nil
}
