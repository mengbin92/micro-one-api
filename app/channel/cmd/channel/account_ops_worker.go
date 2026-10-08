package main

import (
	"github.com/go-kratos/kratos/v3"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/app/channel/internal/server"
	"micro-one-api/app/channel/internal/service"
)

// The scan-only process reuses Wire's repository/usecase construction while
// omitting business transports, registration, outbox and model-sync workers.
func newAccountOpsApp(cfg *Config, repo *data.Repository, uc *biz.ChannelUsecase) (*kratos.App, func()) {
	probe := service.NewCodexModelProbeService(repo)
	probe.SetAnthropicProber(service.NewAnthropicModelProbeService())
	stop := startAccountOpsAutomation(uc, repo, nil, probe, nil)
	app := kratos.New(
		kratos.Name("channel-account-ops-worker"),
		kratos.Server(server.NewAccountOpsHTTPServer(cfg.Server.Http.Addr)),
	)
	return app, stop
}
