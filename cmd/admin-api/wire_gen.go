//go:build !wireinject

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
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
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"

	_ "github.com/go-sql-driver/mysql"
)

func loadConfig(confPath string) (*admincfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
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

	// Setup service discovery
	discovery, dErr := appregistry.NewDiscovery(cfg.Registry)
	if dErr != nil {
		fmt.Printf("Warning: Failed to create service discovery: %v\n", dErr)
	}
	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		fmt.Printf("Warning: Failed to create registrar: %v\n", rErr)
	}

	resolver := appregistry.NewResolver(discovery)
	resolver.SetStatic("identity-service", cfg.Clients.Identity.Endpoint)
	resolver.SetStatic("channel-service", cfg.Clients.Channel.Endpoint)
	resolver.SetStatic("billing-service", cfg.Clients.Billing.Endpoint)

	identityEndpoint, _ := resolver.ResolveGRPC(context.Background(), "identity-service")
	channelEndpoint, _ := resolver.ResolveGRPC(context.Background(), "channel-service")
	billingEndpoint, _ := resolver.ResolveGRPC(context.Background(), "billing-service")

	identityConn, err := grpc.NewClient(identityEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to identity service: %w", err)
	}

	channelConn, err := grpc.NewClient(channelEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		identityConn.Close()
		return nil, nil, fmt.Errorf("failed to connect to channel service: %w", err)
	}

	billingConn, err := grpc.NewClient(billingEndpoint,
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

	httpSrv := adminserver.NewHTTPServer(cfg.Server.HTTP.Addr, adminSvc, cfg.Clients.Identity.HTTPEndpoint)

	kratosOpts := []kratos.Option{
		kratos.Name("admin-api"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

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
