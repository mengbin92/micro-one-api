//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"

	configcfg "micro-one-api/app/config/internal/conf"
	"micro-one-api/app/config/internal/biz"
	"micro-one-api/app/config/internal/data"
	"micro-one-api/app/config/internal/server"
	"micro-one-api/app/config/internal/service"

	"micro-one-api/platform/events"
	appregistry "micro-one-api/platform/registry"
)

// ProviderSet declares config-service providers. loadConfig lives in
// config_loader.go so it is visible under both build tags.
var ProviderSet = wire.NewSet(
	newRepo,
	newEventBus,
	biz.NewConfigUsecase,
	service.NewConfigService,
	server.NewGRPCServer,
	server.NewHTTPServer,
	provideRegistrar,
	wire.Bind(new(biz.ConfigRepo), new(*data.Repository)),
)

// newRepo wraps data.NewRepositoryFromEnv so Wire can resolve it from a
// *configcfg.Config (it passes the configured driver + DSN).
func newRepo(cfg *configcfg.Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
}

// newEventBus builds the EventBus from the repository's Redis client.
func newEventBus(repo *data.Repository) events.EventBus {
	return events.NewConfiguredEventBus(repo.Redis(), "config-service")
}

// registrarResult wraps an optional kratos Registrar so Wire can thread it
// through the dependency graph without nil-interface ambiguity.
type registrarResult struct {
	Registrar registry.Registrar
}

// provideRegistrar builds the optional service registrar. On error a zero
// value is returned so the app can still start without service discovery.
func provideRegistrar(cfg *configcfg.Config) registrarResult {
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

func newApp(cfg *configcfg.Config, svc *service.ConfigService, reg registrarResult) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)
	opts := []kratos.Option{
		kratos.Name("config-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)
	return app, func() {}
}
