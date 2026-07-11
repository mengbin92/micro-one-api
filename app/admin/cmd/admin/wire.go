//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	kregistry "github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admincfg "micro-one-api/app/admin/internal/conf"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"

	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newClients,
	newSystemOptionsRepo,
	newSubscriptionUsecases,
	provideIdentityClient,
	provideChannelClient,
	provideBillingClient,
	provideSystemOptionsStore,
	service.NewAdminService,
	provideRegistrar,
)

func provideIdentityClient(c *clientsResult) identityv1.IdentityServiceClient { return c.identityClient }
func provideChannelClient(c *clientsResult) channelv1.ChannelServiceClient   { return c.channelClient }
func provideBillingClient(c *clientsResult) billingv1.BillingServiceClient   { return c.billingClient }
func provideSystemOptionsStore(r systemOptionsResult) service.SystemOptionsStore { return r.Repo }

type registrarResult struct {
	Registrar kregistry.Registrar
}

func provideRegistrar(cfg *admincfg.Config) registrarResult {
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

func newApp(
	cfg *admincfg.Config,
	clients *clientsResult,
	sub subscriptionResult,
	svc *service.AdminService,
	reg registrarResult,
) (*kratos.App, func()) {
	// Wire optional subscription usecases onto the admin service.
	if sub.SubUc != nil {
		planUc := sub.PlanUc
		svc.SetSubscriptionUsecases(sub.SubUc, sub.GroupUc, planUc)
	}

	grpcSrv := newGRPCServer(cfg, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc, cfg.Clients.Identity.HTTPEndpoint, cfg.Server.HTTP.WebRoot)

	opts := []kratos.Option{
		kratos.Name("admin-api"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)

	startSignalHandler(appSignalStopper{app})

	return app, func() {
		clients.identityConn.Close()
		clients.channelConn.Close()
		clients.billingConn.Close()
	}
}
