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
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/platform/database/xdb"
	applogger "micro-one-api/platform/logging"
	appregistry "micro-one-api/platform/registry"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"

	"github.com/go-kratos/kratos/v3"
	grpcx "github.com/go-kratos/kratos/v3/transport/grpc"
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
func newClients(cfg *Config) (*clientsResult, error) {
	if cfg.Bootstrap == nil || cfg.Bootstrap.Registry == nil || cfg.Bootstrap.Clients == nil {
		return nil, fmt.Errorf("invalid config: missing bootstrap, registry, or clients")
	}

	discovery, dErr := appregistry.NewDiscovery(cfg.Registry())
	if dErr != nil {
		applogger.Log.Warn("failed to create service discovery", zap.Error(dErr))
	}
	registrar, rErr := appregistry.NewRegistrar(cfg.Registry())
	_ = registrar
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
	}

	resolver := appregistry.NewResolver(discovery)
	if cfg.Bootstrap.Clients.Identity != nil {
		resolver.SetStatic("identity-service", cfg.Bootstrap.Clients.Identity.Endpoint)
	}
	if cfg.Bootstrap.Clients.Channel != nil {
		resolver.SetStatic("channel-service", cfg.Bootstrap.Clients.Channel.Endpoint)
	}
	if cfg.Bootstrap.Clients.Billing != nil {
		resolver.SetStatic("billing-service", cfg.Bootstrap.Clients.Billing.Endpoint)
	}

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
	Repo biz.SystemOptionsRepo
}

// newSystemOptionsRepo opens the system-options DB if configured, returning
// a zero value (nil repo) when DB is not available.
func newSystemOptionsRepo(cfg *Config) systemOptionsResult {
	if cfg.Bootstrap == nil || cfg.Bootstrap.Data == nil || cfg.Bootstrap.Data.Database == nil {
		return systemOptionsResult{}
	}
	if cfg.Bootstrap.Data.Database.Source == "" {
		return systemOptionsResult{}
	}
	source := resolveAdminSchemaDSN(cfg.Bootstrap.Data.Database.Driver, cfg.Bootstrap.Data.Database.Source)
	db, dbErr := xdb.OpenSQL(cfg.Bootstrap.Data.Database.Driver, source)
	if dbErr != nil {
		applogger.Log.Warn("failed to connect to system options DB", zap.Error(dbErr))
		return systemOptionsResult{}
	}
	return systemOptionsResult{Repo: data.NewSystemOptionsRepoWithDriver(db, cfg.Bootstrap.Data.Database.Driver)}
}

// subscriptionResult wraps the optional subscription usecase.
type subscriptionResult struct {
	SubUc   *subscriptionbiz.SubscriptionUsecase
	GroupUc *subscriptionbiz.GroupUsecase
	PlanUc  *subscriptionbiz.PlanUsecase
}

// newSubscriptionUsecases opens the subscription DB if configured.
func newSubscriptionUsecases(cfg *Config) subscriptionResult {
	if cfg.Bootstrap == nil || cfg.Bootstrap.Data == nil || cfg.Bootstrap.Data.Database == nil {
		return subscriptionResult{}
	}
	if cfg.Bootstrap.Data.Database.Source == "" {
		return subscriptionResult{}
	}
	driver := cfg.Bootstrap.Data.Database.Driver
	source := resolveAdminSchemaDSN(cfg.Bootstrap.Data.Database.Driver, cfg.Bootstrap.Data.Database.Source)
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
func newGRPCServer(cfg *Config, svc *service.AdminService) *grpcx.Server {
	if cfg.Bootstrap == nil || cfg.Bootstrap.Server == nil || cfg.Bootstrap.Server.Grpc == nil {
		return grpcx.NewServer()
	}
	grpcSrv := grpcx.NewServer(grpcx.Address(cfg.Bootstrap.Server.Grpc.Addr))
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

// resolveAdminSchemaDSN applies the admin-side database schema (Phase 2.4) to
// the configured DSN. Admin owns system_options and shares the subscription
// tables; both are isolated via the same per-service schema. The schema is
// read from cfg.Data.Database.Schema (wire) or ADMIN_SCHEMA / DATABASE_SCHEMA
// env vars for direct callers.
func resolveAdminSchemaDSN(driver, dsn string) string {
	schema := xdb.ResolveSchema("", "ADMIN_SCHEMA", "DATABASE_SCHEMA")
	if schema == "" {
		return dsn
	}
	switch xdb.NormalizeDriver(driver, dsn) {
	case xdb.DriverMySQL:
		if rewritten, err := xdb.RewriteMySQLDBName(dsn, schema); err == nil {
			return rewritten
		}
	case xdb.DriverPostgres:
		if rewritten, err := xdb.RewritePostgresSearchPath(dsn, schema); err == nil {
			return rewritten
		}
	}
	return dsn
}
