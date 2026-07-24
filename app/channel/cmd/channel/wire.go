//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/registry"
	"github.com/google/wire"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/app/channel/internal/server"
	"micro-one-api/app/channel/internal/service"

	"micro-one-api/platform/events"
	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newRepo,
	newEventBus,
	biz.NewChannelUsecase,
	biz.NewModelUsecase,
	biz.NewModelRoutingUsecase,
	service.NewChannelService,
	server.NewGRPCServer,
	server.NewHTTPServer,
	provideRegistrar,
	wire.Bind(new(biz.ChannelRepo), new(*data.Repository)),
	wire.Bind(new(biz.ModelRepo), new(*data.Repository)),
	wire.Bind(new(biz.ModelRoutingRepo), new(*data.Repository)),
)

func newRepo(cfg *Config) (*data.Repository, error) {
	return data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source, cfg.Data.Database.Schema)
}

func newEventBus(repo *data.Repository) events.EventBus {
	return events.NewConfiguredEventBus(repo.Redis(), "channel-service")
}

type registrarResult struct {
	Registrar registry.Registrar
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
	repo *data.Repository,
	eventBus events.EventBus,
	uc *biz.ChannelUsecase,
	modelUC *biz.ModelUsecase,
	routingUC *biz.ModelRoutingUsecase,
	svc *service.ChannelService,
	reg registrarResult,
) (*kratos.App, func()) {
	svc.SetModelUsecase(modelUC)
	svc.SetModelRoutingUsecase(routingUC)
	uc.SetModelRoutingRepo(repo)
	grpcSrv := server.NewGRPCServer(cfg.Server.Grpc.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.Http.Addr, svc.Usecase())

	var stopEventBus func()
	var modelProbe *service.CodexModelProbeService
	if probe := service.NewCodexModelProbeService(repo); probe != nil {
		// Route domestic Anthropic-compatible Coding Plan platforms
		// (zhipu/minimax/kimi) to the Messages-API prober so newly added
		// accounts get their supported-model list refreshed too. Previously
		// only codex accounts were probed, leaving the domestic three stuck
		// with whatever models were typed at creation time.
		probe.SetAnthropicProber(service.NewAnthropicModelProbeService())
		modelProbe = probe
		eventBus.Subscribe(events.TopicChannelChanged, probe.HandleSubscriptionAccountEvent)
		probe.SyncExistingCodexAccounts(context.Background(), repo)
	}
	var quotaProbe *service.CodingPlanQuotaProbeService
	if probe := service.NewCodingPlanQuotaProbeService(repo, service.CodingPlanQuotaProbeConfig{
		Enabled:  envBool("CODING_PLAN_QUOTA_PROBE_ENABLED", false),
		Interval: parseDurationEnv("CODING_PLAN_QUOTA_PROBE_INTERVAL", 5*time.Minute),
		Timeout:  parseDurationEnv("CODING_PLAN_QUOTA_PROBE_TIMEOUT", 30*time.Second),
		PageSize: 200,
	}); probe != nil {
		quotaProbe = probe
	}
	if streamBus, ok := eventBus.(interface {
		StartListening(context.Context) func()
	}); ok {
		stopEventBus = streamBus.StartListening(context.Background())
	}
	notifyConn, err := configureHealthAlert(uc)
	if err != nil {
		// In production this would abort; for wire we just proceed.
		_ = err
	}
	stopOpsAutomation := startAccountOpsAutomation(uc, repo, notifyConn, modelProbe, quotaProbe)

	opts := []kratos.Option{
		kratos.Name("channel-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)

	return app, func() {
		if stopOpsAutomation != nil {
			stopOpsAutomation()
		}
		if stopEventBus != nil {
			stopEventBus()
		}
		if notifyConn != nil {
			_ = notifyConn.Close()
		}
	}
}
