//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-kratos/kratos/v2"
	kregistry "github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/billing/internal/biz"
	bcfg "micro-one-api/app/billing/internal/conf"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/app/billing/internal/server"
	"micro-one-api/app/billing/internal/service"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	applogger "micro-one-api/platform/logging"
	appregistry "micro-one-api/platform/registry"
)

var ProviderSet = wire.NewSet(
	newData,
	provideRegistrar,
)

func newData(cfg *bcfg.Config) (*data.Data, error) {
	return data.NewData(cfg.Data.Database.Driver, cfg.Data.Database.Source)
}

type registrarResult struct {
	Registrar kregistry.Registrar
}

func provideRegistrar(cfg *bcfg.Config) registrarResult {
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

func newApp(cfg *bcfg.Config, d *data.Data, reg registrarResult) (*kratos.App, func()) {
	pricing := biz.PricingConfig{
		GroupRatios:      cfg.Billing.GroupRatios,
		ModelRatios:      cfg.Billing.ModelRatios,
		CompletionRatios: cfg.Billing.CompletionRatios,
		PricingStore:     d.PricingConfigStore(),
	}
	if len(pricing.GroupRatios) == 0 {
		pricing.GroupRatios = biz.DefaultGroupRatios()
	}
	subscriptionRepo := subscriptiondata.NewRepository(d.DB(), d.Redis())
	subscriptionUc := subscriptionbiz.NewSubscriptionUsecase(subscriptionRepo, subscriptionRepo)
	uc := biz.NewBillingUsecaseWithPricing(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.LedgerRepo(),
		d.RedeemRepo(),
		pricing,
	)
	uc.SetSubscriptionPrimatives(subscriptionUc)
	uc.SetReceivableRepo(d.ReceivableRepo())
	var asyncBilling *biz.AsyncBillingUsecase
	if cfg.Billing.Async.Enabled {
		asyncBilling = biz.NewAsyncBillingUsecase(
			uc,
			d.Redis(),
			defaultInt(cfg.Billing.Async.QueueSize, 1000),
			defaultInt(cfg.Billing.Async.BatchSize, 100),
			parseDurationOrDefault(cfg.Billing.Async.BatchInterval, 5*time.Second),
		)
	}
	reconUc := biz.NewReconciliationUsecase(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.ReconciliationRepo(),
		d.ReconciliationRunStore(),
	)
	paymentProvider := biz.NewConfiguredPaymentProvider(cfg.Payment)
	paymentAssetIssuer := biz.NewPaymentAssetIssuer(uc)
	paymentSubscriptionAssigner := biz.NewPaymentSubscriptionAssigner(subscriptionUc, subscriptionRepo, subscriptionRepo)
	planSnapshotter := biz.NewPaymentPlanSnapshotter(subscriptionRepo)
	paymentUc := biz.NewPaymentUsecaseWithAssignerAndSnapshotter(d.PaymentRepo(), paymentProvider, paymentAssetIssuer, paymentSubscriptionAssigner, planSnapshotter)
	alipayVerifier := biz.NewAlipayPaymentProvider(cfg.Payment.Alipay)
	svc := service.NewBillingService(uc, reconUc, paymentUc, alipayVerifier)
	// Phase 2: refund/reversal coordinator. The subscription reverter delegates
	// to the subscription usecase so revoke/shorten mutations land on the same
	// row the assigner created. The operational report builder aggregates
	// payment_orders + user_subscriptions so the dashboard never samples.
	refundUc := biz.NewRefundUsecase(d.PaymentRepo(), d.AccountRepo(), d.LedgerRepo(), subscriptionUc)
	svc.SetRefundUsecase(refundUc)
	reportUc := biz.NewSubscriptionReportUsecase(data.NewOperationReportRepo(d))
	svc.SetSubscriptionReportUsecase(reportUc)
	// Phase 2.1: wire the async billing coordinator into the service so
	// CommitQuota can enqueue settlement and return a provisional response
	// when cfg.Billing.Async.Enabled. asyncBilling is nil when disabled.
	svc.SetAsyncBillingUsecase(asyncBilling)

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
			var err error
			notifyConn, err = grpc.NewClient(cfg.Clients.Notify.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				applogger.Log.Error("dial notify endpoint", zap.Error(err))
			} else {
				client := notifyv1.NewNotifyServiceClient(notifyConn)
				notifier = newGRPCNotifier(client, cfg.Clients.Notify.NotifyType)
			}
		}
	}

	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP.Addr, svc)

	opts := []kratos.Option{
		kratos.Name("billing-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if reg.Registrar != nil {
		opts = append(opts, kratos.Registrar(reg.Registrar))
	}
	app := kratos.New(opts...)

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
	partitionStop := startPartitionMaintenance(ctx, d.DB(), cfg.Partition)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		cancel()
		cleanupJob.Stop()
		reconJob.Stop()
		if partitionStop != nil {
			partitionStop()
		}
	}()

	_ = fmt.Sprintf // keep fmt import used; the real error paths are handled above
	return app, func() {
		d.Close()
		cancel()
		if asyncBilling != nil {
			_ = asyncBilling.Close()
		}
		if partitionStop != nil {
			partitionStop()
		}
		if notifyConn != nil {
			_ = notifyConn.Close()
		}
	}
}
