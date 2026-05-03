//go:build !wireinject

package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	grpcx "github.com/go-kratos/kratos/v2/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admincfg "micro-one-api/internal/admin/config"
	"micro-one-api/internal/admin/data"
	adminserver "micro-one-api/internal/admin/server"
	"micro-one-api/internal/admin/service"

	_ "github.com/go-sql-driver/mysql"
)

func loadConfig(confPath string) (*admincfg.Config, error) {
	source := file.NewSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg admincfg.Config
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

	identityConn, err := grpc.NewClient(cfg.Clients.Identity.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to identity service: %w", err)
	}

	channelConn, err := grpc.NewClient(cfg.Clients.Channel.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		identityConn.Close()
		return nil, nil, fmt.Errorf("failed to connect to channel service: %w", err)
	}

	billingConn, err := grpc.NewClient(cfg.Clients.Billing.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		identityConn.Close()
		channelConn.Close()
		return nil, nil, fmt.Errorf("failed to connect to billing service: %w", err)
	}

	identityClient := identityv1.NewIdentityServiceClient(identityConn)
	channelClient := channelv1.NewChannelServiceClient(channelConn)
	billingClient := billingv1.NewBillingServiceClient(billingConn)

	// System options repo (optional — skip if DB not configured)
	var systemOptsRepo service.SystemOptionsStore
	if cfg.Data.Database.Source != "" {
		db, dbErr := sql.Open(cfg.Data.Database.Driver, cfg.Data.Database.Source)
		if dbErr == nil {
			systemOptsRepo = data.NewSystemOptionsRepo(db)
		} else {
			fmt.Printf("Warning: Failed to connect to system options DB: %v\n", dbErr)
		}
	}

	adminSvc := service.NewAdminService(billingClient, identityClient, channelClient, systemOptsRepo)

	grpcSrv := grpcx.NewServer(grpcx.Address(cfg.Server.GRPC.Addr))
	adminv1.RegisterAdminServiceServer(grpcSrv, adminSvc)

	httpSrv := adminserver.NewHTTPServer(cfg.Server.HTTP.Addr)

	app := kratos.New(
		kratos.Name("admin-api"),
		kratos.Server(grpcSrv, httpSrv),
	)

	cleanup := func() {
		identityConn.Close()
		channelConn.Close()
		billingConn.Close()
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		app.Stop()
	}()

	return app, cleanup, nil
}
