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

	"github.com/go-kratos/kratos/v3"
	kregistry "github.com/go-kratos/kratos/v3/registry"
	"github.com/google/wire"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/app/billing/internal/server"
	"micro-one-api/app/billing/internal/service"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	applogger "micro-one-api/platform/logging"
	appregistry "micro-one-api/platform/registry"

	grpcx "github.com/go-kratos/kratos/v3/transport/grpc"
	httpx "github.com/go-kratos/kratos/v3/transport/http"
)

var ProviderSet = wire.NewSet(
	newData,
	provideRegistrar,
)

func newData(cfg *Config) (*data.Data, error) {
	return data.NewData(cfg.Bootstrap.Data.Database.Driver, cfg.Bootstrap.Data.Database.Source, cfg.Bootstrap.Data.Database.Schema)
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

func newApp(cfg *Config, d *data.Data, reg registrarResult) (*kratos.App, func()) {
	if cfg.Bootstrap == nil {
		panic("config bootstrap is nil")
	}

	// Build pricing config with nil-safe defaults.
	pricing := biz.PricingConfig{
		PricingStore: d.PricingConfigStore(),
	}
	if cfg.Bootstrap.Billing != nil {
		pricing.GroupRatios = cfg.Bootstrap.Billing.GroupRatios
		pricing.ModelRatios = cfg.Bootstrap.Billing.ModelRatios
		pricing.CompletionRatios = cfg.Bootstrap.Billing.CompletionRatios
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
	if cfg.Bootstrap.Billing != nil && cfg.Bootstrap.Billing.Async != nil && cfg.Bootstrap.Billing.Async.Enabled {
		asyncCfg := cfg.Bootstrap.Billing.Async
		asyncBilling = biz.NewAsyncBillingUsecase(
			uc,
			d.Redis(),
			defaultInt(int(asyncCfg.QueueSize), 1000),
			defaultInt(int(asyncCfg.BatchSize), 100),
			parseDurationOrDefault(asyncCfg.BatchInterval, 5*time.Second),
		)
	}

	reconUc := biz.NewReconciliationUsecase(
		d.AccountRepo(),
		d.ReservationRepo(),
		d.ReconciliationRepo(),
		d.ReconciliationRunStore(),
	)

	var paymentProvider biz.PaymentProvider
	if cfg.Bootstrap.Payment != nil {
		paymentProvider = biz.NewConfiguredPaymentProvider(cfg.Bootstrap.Payment.ToPaymentConfig())
	} else {
		paymentProvider = biz.NewConfiguredPaymentProvider(biz.PaymentConfig{})
	}
	paymentAssetIssuer := biz.NewPaymentAssetIssuer(uc)
	paymentSubscriptionAssigner := biz.NewPaymentSubscriptionAssigner(subscriptionUc, subscriptionRepo, subscriptionRepo)
	planSnapshotter := biz.NewPaymentPlanSnapshotter(subscriptionRepo)
	paymentUc := biz.NewPaymentUsecaseWithAssignerAndSnapshotter(d.PaymentRepo(), paymentProvider, paymentAssetIssuer, paymentSubscriptionAssigner, planSnapshotter)

	var alipayVerifier biz.PaymentNotifyVerifier
	if cfg.Bootstrap.Payment != nil && cfg.Bootstrap.Payment.Alipay != nil {
		alipayVerifier = biz.NewAlipayPaymentProvider(cfg.Bootstrap.Payment.ToPaymentConfig().Alipay)
	} else {
		alipayVerifier = biz.NewAlipayPaymentProvider(biz.AlipayConfig{})
	}
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
	if cfg.Bootstrap.Recon != nil && cfg.Bootstrap.Recon.Enabled {
		if cfg.Bootstrap.Recon.Interval != "" {
			if d, parseErr := time.ParseDuration(cfg.Bootstrap.Recon.Interval); parseErr == nil && d > 0 {
				interval = d
			}
		}
		if len(cfg.Bootstrap.Recon.Recipients) > 0 {
			recipients = cfg.Bootstrap.Recon.Recipients
		}
		if cfg.Bootstrap.Clients != nil && cfg.Bootstrap.Clients.Notify != nil && cfg.Bootstrap.Clients.Notify.Endpoint != "" {
			var err error
			notifyConn, err = grpc.NewClient(cfg.Bootstrap.Clients.Notify.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				applogger.Log.Error("dial notify endpoint", zap.Error(err))
			} else {
				client := notifyv1.NewNotifyServiceClient(notifyConn)
				notifyType := ""
				if cfg.Bootstrap.Clients.Notify != nil {
					notifyType = cfg.Bootstrap.Clients.Notify.NotifyType
				}
				notifier = newGRPCNotifier(client, notifyType)
			}
		}
	}

	var grpcSrv *grpcx.Server = nil
	var httpSrv *httpx.Server = nil
	if cfg.Bootstrap.Server != nil {
		if cfg.Bootstrap.Server.Grpc != nil {
			grpcSrv = server.NewGRPCServer(cfg.Bootstrap.Server.Grpc.Addr, svc)
		}
		if cfg.Bootstrap.Server.Http != nil {
			httpSrv = server.NewHTTPServer(cfg.Bootstrap.Server.Http.Addr, svc)
		}
	}

	opts := []kratos.Option{
		kratos.Name("billing-service"),
	}
	if grpcSrv != nil {
		opts = append(opts, kratos.Server(grpcSrv))
	}
	if httpSrv != nil {
		if grpcSrv == nil {
			opts = append(opts, kratos.Server(httpSrv))
		} else {
			opts[len(opts)-1] = kratos.Server(grpcSrv, httpSrv)
		}
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
	}
	if cfg.Bootstrap.Clients != nil && cfg.Bootstrap.Clients.Notify != nil {
		reconJobOpts = append(reconJobOpts, biz.WithNotifyType(cfg.Bootstrap.Clients.Notify.NotifyType))
	}
	reconJob := biz.NewReconciliationJob(reconUc, interval, reconJobOpts...)
	go cleanupJob.Start(ctx)
	go reconJob.Start(ctx)
	partitionStop := startPartitionMaintenance(ctx, d.DB(), cfg.Bootstrap.Partition)
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
