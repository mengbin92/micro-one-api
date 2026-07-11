package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admincfg "micro-one-api/app/admin/internal/conf"
	"micro-one-api/app/admin/internal/data"
	"micro-one-api/app/admin/internal/service"
	applogger "micro-one-api/platform/logging"
	appregistry "micro-one-api/platform/registry"
	"micro-one-api/platform/database/xdb"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"

	"github.com/go-kratos/kratos/v2"
	grpcx "github.com/go-kratos/kratos/v2/transport/grpc"
)

// clientsResult bundles all downstream gRPC clients and their connections.
type clientsResult struct {
	identityClient identityv1.IdentityServiceClient
	channelClient  channelv1.ChannelServiceClient
	billingClient  billingv1.BillingServiceClient

	identityConn *grpc.ClientConn
	channelConn  *grpc.ClientConn
	billingConn  *grpc.ClientConn
}

// newClients dials the identity, channel, and billing services via the
// resolver, returning proto clients and their underlying connections.
func newClients(cfg *admincfg.Config) (*clientsResult, error) {
	discovery, dErr := appregistry.NewDiscovery(cfg.Registry)
	if dErr != nil {
		applogger.Log.Warn("failed to create service discovery", zap.Error(dErr))
	}
	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	_ = registrar
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
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
		return nil, fmt.Errorf("failed to connect to identity service: %w", err)
	}

	channelConn, err := grpc.NewClient(channelEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		identityConn.Close()
		return nil, fmt.Errorf("failed to connect to channel service: %w", err)
	}

	billingConn, err := grpc.NewClient(billingEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		identityConn.Close()
		channelConn.Close()
		return nil, fmt.Errorf("failed to connect to billing service: %w", err)
	}

	return &clientsResult{
		identityClient: identityv1.NewIdentityServiceClient(identityConn),
		channelClient:  channelv1.NewChannelServiceClient(channelConn),
		billingClient:  billingv1.NewBillingServiceClient(billingConn),
		identityConn:   identityConn,
		channelConn:    channelConn,
		billingConn:    billingConn,
	}, nil
}

// systemOptionsResult wraps the optional system-options store.
type systemOptionsResult struct {
	Repo service.SystemOptionsStore
}

// newSystemOptionsRepo opens the system-options DB if configured, returning
// a zero value (nil repo) when DB is not available.
func newSystemOptionsRepo(cfg *admincfg.Config) systemOptionsResult {
	if cfg.Data.Database.Source == "" {
		return systemOptionsResult{}
	}
	db, dbErr := xdb.OpenSQL(cfg.Data.Database.Driver, cfg.Data.Database.Source)
	if dbErr != nil {
		applogger.Log.Warn("failed to connect to system options DB", zap.Error(dbErr))
		return systemOptionsResult{}
	}
	return systemOptionsResult{Repo: data.NewSystemOptionsRepoWithDriver(db, cfg.Data.Database.Driver)}
}

// subscriptionResult wraps the optional subscription usecase.
type subscriptionResult struct {
	SubUc   *subscriptionbiz.SubscriptionUsecase
	GroupUc *subscriptionbiz.GroupUsecase
	PlanUc  *subscriptionbiz.PlanUsecase
}

// newSubscriptionUsecases opens the subscription DB if configured.
func newSubscriptionUsecases(cfg *admincfg.Config) subscriptionResult {
	if cfg.Data.Database.Source == "" {
		return subscriptionResult{}
	}
	driver := cfg.Data.Database.Driver
	source := cfg.Data.Database.Source
	repo, subErr := subscriptiondata.NewRepositoryFromEnv(driver, source)
	if subErr != nil {
		applogger.Log.Warn("failed to connect to subscription DB", zap.Error(subErr))
		return subscriptionResult{}
	}
	return subscriptionResult{
		SubUc:   subscriptionbiz.NewSubscriptionUsecase(repo, repo),
		GroupUc: subscriptionbiz.NewGroupUsecase(repo),
		PlanUc:  subscriptionbiz.NewPlanUsecase(repo, repo),
	}
}

// newGRPCServer creates the admin gRPC server and registers the AdminService.
func newGRPCServer(cfg *admincfg.Config, svc *service.AdminService) *grpcx.Server {
	grpcSrv := grpcx.NewServer(grpcx.Address(cfg.Server.GRPC.Addr))
	adminv1.RegisterAdminServiceServer(grpcSrv, svc)
	return grpcSrv
}

// startSignalHandler starts a goroutine that listens for SIGINT/SIGTERM and
// stops the provided app.
func startSignalHandler(app interface{ Stop() }) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		app.Stop()
	}()
}

// appSignalStopper wraps *kratos.App so it satisfies the stopSignal interface
// (kratos.App.Stop returns an error, while our signal handler expects no return).
type appSignalStopper struct {
	app *kratos.App
}

func (s appSignalStopper) Stop() {
	_ = s.app.Stop()
}
