//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	kregistry "github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"

	"micro-one-api/app/identity/internal/biz"
	identitycfg "micro-one-api/app/identity/internal/conf"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/app/identity/internal/server"
	"micro-one-api/app/identity/internal/service"

	appregistry "micro-one-api/platform/registry"
	"micro-one-api/platform/security"
)

var ProviderSet = wire.NewSet(
	newRepo,
	biz.NewIdentityUsecase,
	service.NewIdentityService,
	server.NewGRPCServer,
	provideRegistrar,
	wire.Bind(new(biz.IdentityRepo), new(*data.Repository)),
)

func newRepo(cfg *identitycfg.Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
}

type registrarResult struct {
	Registrar kregistry.Registrar
}

func provideRegistrar(cfg *identitycfg.Config) registrarResult {
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
		setupOAuth,
		newApp,
	))
}

func newApp(
	cfg *identitycfg.Config,
	uc *biz.IdentityUsecase,
	svc *service.IdentityService,
	oauthRegistry *oauth.ProviderRegistry,
	reg registrarResult,
) (*kratos.App, func()) {
	bootstrapAdmin(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	billingClient, billingConn, _ := newBillingClient(cfg)
	httpSrv := server.NewHTTPServerWithRegistrationPolicy(
		cfg.Server.HTTP.Addr, uc, oauthRegistry,
		registrationPolicyFromConfig(cfg), billingClient,
	)
	opts := []kratos.Option{
		kratos.Name("identity-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)
	return app, func() {
		if billingConn != nil {
			billingConn.Close()
		}
	}
}
