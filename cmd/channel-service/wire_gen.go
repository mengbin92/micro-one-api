//go:build !wireinject

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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

	repo, err := data.NewRepositoryFromEnv(cfg.Data.Database.Driver, cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	eventBus := events.NewConfiguredEventBus(repo.Redis(), "channel-service")
	uc := biz.NewChannelUsecase(repo, eventBus)
	var stopEventBus func()
	if probe := service.NewCodexModelProbeService(repo); probe != nil {
		eventBus.Subscribe(events.TopicChannelChanged, probe.HandleSubscriptionAccountEvent)
		probe.SyncExistingCodexAccounts(context.Background(), repo)
	}
	if streamBus, ok := eventBus.(interface {
		StartListening(context.Context) func()
	}); ok {
		stopEventBus = streamBus.StartListening(context.Background())
	}
	notifyConn, err := configureHealthAlert(uc)
	if err != nil {
		return nil, nil, err
	}
	stopOpsAutomation := startAccountOpsAutomation(uc, repo, notifyConn)
	svc := service.NewChannelService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, uc)

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
		if stopOpsAutomation != nil {
			stopOpsAutomation()
		}
		if stopEventBus != nil {
			stopEventBus()
		}
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

// startAccountOpsAutomation launches the subscription-account governance
// background tasks (quota reset sweeper, account recovery sweeper, quota alert
// evaluator) when enabled via environment variables. These run in-process in
// channel-service because they need direct ChannelRepo access (the Repository
// implements both ChannelRepo and QuotaResetRunRecorder). The alert evaluator
// reuses the notify-worker gRPC connection so no new delivery path is created.
//
// Returns a cleanup function that cancels the background context and closes
// the notify connection if one was opened. Safe to call with a nil uc.
func startAccountOpsAutomation(uc *biz.ChannelUsecase, repo biz.ChannelRepo, existingNotifyConn *grpc.ClientConn) func() {
	var (
		cancel func()
		wg     sync.WaitGroup
		conn   = existingNotifyConn
	)
	if uc == nil {
		return func() {}
	}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn

	// 1. Quota reset sweeper (fixed-strategy daily/weekly boundary reset).
	if envBool("SUBSCRIPTION_QUOTA_RESET_ENABLED", false) {
		interval := parseDurationEnv("SUBSCRIPTION_QUOTA_RESET_INTERVAL", 5*time.Minute)
		timeout := parseDurationEnv("SUBSCRIPTION_QUOTA_RESET_TIMEOUT", 30*time.Second)
		sweeper := biz.NewQuotaResetSweeper(repo, repo, biz.QuotaResetSweeperConfig{
			Enabled:  true,
			Interval: interval,
			Timeout:  timeout,
			PageSize: 200,
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			sweeper.Run(ctx)
		}()
		applogger.Log.Info("subscription quota reset sweeper started",
			zap.Duration("interval", interval))
	}

	// 2. Account recovery sweeper (auto-recover temp-blocked accounts after TTL).
	if envBool("SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED", false) {
		interval := parseDurationEnv("SUBSCRIPTION_ACCOUNT_RECOVERY_INTERVAL", 5*time.Minute)
		timeout := parseDurationEnv("SUBSCRIPTION_ACCOUNT_RECOVERY_TIMEOUT", 30*time.Second)
		recovery := biz.NewAccountRecoverySweeper(repo, biz.AccountRecoverySweeperConfig{
			Enabled:  true,
			Interval: interval,
			Timeout:  timeout,
			PageSize: 200,
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			recovery.Run(ctx)
		}()
		applogger.Log.Info("subscription account recovery sweeper started",
			zap.Duration("interval", interval))
	}

	// 3. Quota alert evaluator (reuses notify-worker channel for delivery).
	if envBool("SUBSCRIPTION_QUOTA_ALERT_ENABLED", false) {
		endpoint := strings.TrimSpace(os.Getenv("NOTIFY_GRPC_ENDPOINT"))
		var notifier biz.QuotaAlertNotifier
		if endpoint != "" {
			if conn == nil {
				c, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					applogger.Log.Warn("failed to dial notify for quota alert", zap.Error(err))
				} else {
					conn = c
				}
			}
			if conn != nil {
				notifyType := strings.TrimSpace(os.Getenv("CHANNEL_HEALTH_ALERT_NOTIFY_TYPE"))
				notifier = newGRPCNotifier(notifyv1.NewNotifyServiceClient(conn), notifyType)
			}
		}
		if notifier != nil {
			interval := parseDurationEnv("SUBSCRIPTION_QUOTA_ALERT_INTERVAL", 10*time.Minute)
			alertCfg := biz.HealthAlertConfig{
				Enabled:    true,
				NotifyType: strings.TrimSpace(os.Getenv("CHANNEL_HEALTH_ALERT_NOTIFY_TYPE")),
				Recipients: recipientsFromEnv("CHANNEL_HEALTH_ALERT_RECIPIENTS"),
			}
			evaluator := biz.NewQuotaAlertEvaluator(repo, notifier, alertCfg, biz.QuotaAlertEvaluatorConfig{
				Enabled:  true,
				Interval: interval,
				PageSize: 200,
			})
			wg.Add(1)
			go func() {
				defer wg.Done()
				evaluator.Run(ctx)
			}()
			applogger.Log.Info("subscription quota alert evaluator started",
				zap.Duration("interval", interval))
		} else {
			applogger.Log.Warn("subscription quota alert enabled but no notify endpoint configured")
		}
	}

	return func() {
		if cancel != nil {
			cancel()
		}
		wg.Wait()
	}
}

// parseDurationEnv reads a duration from an env var, falling back to def.
func parseDurationEnv(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return def
}
