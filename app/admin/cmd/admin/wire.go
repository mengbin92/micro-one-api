//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v3"
	kregistry "github.com/go-kratos/kratos/v3/registry"
	"github.com/google/wire"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newClients,
	newSystemOptionsRepo,
	newSystemOptionsUsecase,
	newSubscriptionUsecases,
	provideIdentityClient,
	provideChannelClient,
	provideBillingClient,
	service.NewAdminService,
	provideRegistrar,
)

func provideIdentityClient(c *clientsResult) identityv1.IdentityServiceClient {
	return c.identityClient
}
func provideChannelClient(c *clientsResult) channelv1.ChannelServiceClient { return c.channelClient }
func provideBillingClient(c *clientsResult) billingv1.BillingServiceClient { return c.billingClient }
func newSystemOptionsUsecase(r systemOptionsResult) *biz.SystemOptionsUsecase {
	if r.Repo == nil {
		return nil
	}
	return biz.NewSystemOptionsUsecase(r.Repo)
}

type registrarResult struct {
	Registrar kregistry.Registrar
}

func provideRegistrar(cfg *Config) registrarResult {
	registrar, err := appregistry.NewRegistrar(cfg.Registry())
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
	cfg *Config,
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

	// Build HTTP server with nil-safety checks.
	var httpSrv *khttp.Server
	if cfg.Bootstrap != nil && cfg.Bootstrap.Server != nil && cfg.Bootstrap.Server.Http != nil &&
		cfg.Bootstrap.Clients != nil && cfg.Bootstrap.Clients.Identity != nil {
		httpSrv = server.NewHTTPServer(cfg.Bootstrap.Server.Http.Addr, svc, cfg.Bootstrap.Clients.Identity.HttpEndpoint, cfg.Bootstrap.Server.Http.WebRoot)
	} else {
		httpSrv = server.NewHTTPServer("", svc, "", "")
	}

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
		if clients != nil {
			clients.identityConn.Close()
			clients.channelConn.Close()
			clients.billingConn.Close()
		}
	}
}
