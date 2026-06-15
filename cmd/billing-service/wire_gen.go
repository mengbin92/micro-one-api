//go:build !wireinject

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/internal/billing/biz"
	bcfg "micro-one-api/internal/billing/config"
	"micro-one-api/internal/billing/data"
	"micro-one-api/internal/billing/server"
	"micro-one-api/internal/billing/service"
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"
)

func loadConfig(confPath string) (*bcfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg bcfg.Config
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

	d, err := data.NewData(cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	pricing := biz.PricingConfig{
		GroupRatios:      cfg.Billing.GroupRatios,
		ModelRatios:      cfg.Billing.ModelRatios,
		CompletionRatios: cfg.Billing.CompletionRatios,
		PricingStore:     d.PricingConfigStore(),
	}
	if len(pricing.GroupRatios) == 0 {
		pricing.GroupRatios = biz.DefaultGroupRatios()
	}
	uc := biz.NewBillingUsecaseWithPricing(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.LedgerRepo(),
		d.RedeemRepo(),
		pricing,
	)
	reconUc := biz.NewReconciliationUsecase(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.ReconciliationRepo(),
		d.ReconciliationRunStore(),
	)
	paymentProvider := biz.NewConfiguredPaymentProvider(cfg.Payment)
	paymentAssetIssuer := biz.NewPaymentAssetIssuer(uc)
	paymentUc := biz.NewPaymentUsecase(d.PaymentRepo(), paymentProvider, paymentAssetIssuer)
	alipayVerifier := biz.NewAlipayPaymentProvider(cfg.Payment.Alipay)
	svc := service.NewBillingService(uc, reconUc, paymentUc, alipayVerifier)

	// Build the optional notify-worker gRPC client. When the endpoint is empty
	// or alerts are disabled, the job receives a noop notifier so legacy log
	// behaviour is preserved.
	var notifyConn *grpc.ClientConn
	var notifier biz.Notifier = biz.NoopNotifier()
	interval := 1 * time.Hour
	recipients := []string{""}
	if cfg.Recon.Enabled {
		if cfg.Recon.Interval != "" {
			if d, parseErr := time.ParseDuration(cfg.Recon.Interval); parseErr == nil && d > 0 {
				interval = d
			}
		}
		if len(cfg.Recon.Recipients) > 0 {
			recipients = cfg.Recon.Recipients
		}
		if cfg.Clients.Notify.Endpoint != "" {
			notifyConn, err = grpc.NewClient(cfg.Clients.Notify.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return nil, nil, fmt.Errorf("dial notify endpoint: %w", err)
			}
			client := notifyv1.NewNotifyServiceClient(notifyConn)
			notifier = newGRPCNotifier(client, cfg.Clients.Notify.NotifyType)
		}
	}

	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)

	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		fmt.Printf("Warning: Failed to create registrar: %v\n", rErr)
	}

	kratosOpts := []kratos.Option{
		kratos.Name("billing-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	ctx, cancel := context.WithCancel(context.Background())
	cleanupJob := biz.NewCleanupJob(uc, 1*time.Minute)
	reconJobOpts := []biz.JobOption{
		biz.WithNotifier(notifier),
		biz.WithRecipients(recipients),
		biz.WithNotifyType(cfg.Clients.Notify.NotifyType),
	}
	reconJob := biz.NewReconciliationJob(reconUc, interval, reconJobOpts...)
	go cleanupJob.Start(ctx)
	go reconJob.Start(ctx)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		cancel()
		cleanupJob.Stop()
		reconJob.Stop()
	}()

	return app, func() {
		d.Close()
		cancel()
		if notifyConn != nil {
			_ = notifyConn.Close()
		}
	}, nil
}
