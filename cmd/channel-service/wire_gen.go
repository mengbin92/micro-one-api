//go:build !wireinject

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/internal/channel/biz"
	channelcfg "micro-one-api/internal/channel/config"
	"micro-one-api/internal/channel/data"
	"micro-one-api/internal/channel/server"
	"micro-one-api/internal/channel/service"
	"micro-one-api/internal/pkg/events"
	applogger "micro-one-api/internal/pkg/logger"
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"
)

func loadConfig(confPath string) (*channelcfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg channelcfg.Config
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

	repo, err := data.NewRepositoryFromEnv(cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	eventBus := events.NewConfiguredEventBus(repo.Redis(), "channel-service")
	uc := biz.NewChannelUsecase(repo, eventBus)
	notifyConn, err := configureHealthAlert(uc)
	if err != nil {
		return nil, nil, err
	}
	svc := service.NewChannelService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
	}

	kratosOpts := []kratos.Option{
		kratos.Name("channel-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	return app, func() {
		if notifyConn != nil {
			_ = notifyConn.Close()
		}
	}, nil
}

func configureHealthAlert(uc *biz.ChannelUsecase) (*grpc.ClientConn, error) {
	if !envBool("CHANNEL_HEALTH_ALERT_ENABLED", false) {
		return nil, nil
	}
	endpoint := strings.TrimSpace(os.Getenv("NOTIFY_GRPC_ENDPOINT"))
	if endpoint == "" {
		applogger.Log.Warn("channel health alert enabled but NOTIFY_GRPC_ENDPOINT is empty")
		return nil, nil
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial notify endpoint: %w", err)
	}
	notifyType := strings.TrimSpace(os.Getenv("CHANNEL_HEALTH_ALERT_NOTIFY_TYPE"))
	recipients := recipientsFromEnv("CHANNEL_HEALTH_ALERT_RECIPIENTS")
	uc.ConfigureHealthAlert(newGRPCNotifier(notifyv1.NewNotifyServiceClient(conn), notifyType), biz.HealthAlertConfig{
		Enabled:    true,
		NotifyType: notifyType,
		Recipients: recipients,
	})
	return conn, nil
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func recipientsFromEnv(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return []string{""}
	}
	var recipients []string
	if err := json.Unmarshal([]byte(value), &recipients); err == nil {
		return cleanRecipients(recipients)
	}
	return cleanRecipients(strings.Split(value, ","))
}

func cleanRecipients(input []string) []string {
	recipients := make([]string, 0, len(input))
	for _, recipient := range input {
		trimmed := strings.TrimSpace(recipient)
		if trimmed != "" {
			recipients = append(recipients, trimmed)
		}
	}
	if len(recipients) == 0 {
		return []string{""}
	}
	return recipients
}
