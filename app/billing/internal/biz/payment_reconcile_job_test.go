package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"micro-one-api/platform/metrics"
)

type failingPaymentScanRepo struct {
	*memoryPaymentRepo
	err        error
	beforeList func()
}

func (r *failingPaymentScanRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	if r.beforeList != nil {
		r.beforeList()
	}
	if r.err != nil {
		return nil, 0, r.err
	}
	return r.memoryPaymentRepo.ListOrders(ctx, req)
}

func TestPaymentReconcileJobFailureAndRecoveryMetrics(t *testing.T) {
	for _, failure := range []string{"scan", "query", "query_timeout", "issuance"} {
		t.Run(failure, func(t *testing.T) {
			repo := &failingPaymentScanRepo{memoryPaymentRepo: &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-JOB")}}
			provider := &statusPaymentProvider{status: &PaymentProviderStatus{Paid: true, ProviderTradeNo: "ALI-JOB"}}
			issuer := &flakyPaymentIssuer{delegate: &countingPaymentIssuer{}, err: errors.New("issuance failed")}
			result := "partial"
			switch failure {
			case "scan":
				repo.err = errors.New("scan failed")
				result = "error"
			case "query":
				provider.err = errors.New("query failed")
			case "query_timeout":
				provider.err = context.DeadlineExceeded
			case "issuance":
				issuer.failures = 1
			}
			job := NewPaymentReconcileJob(NewPaymentUsecase(repo, provider, issuer), time.Minute)
			before := map[string]float64{}
			for _, label := range []string{"success", "partial", "error"} {
				before[label] = testutil.ToFloat64(metrics.PaymentReconcileRuns.WithLabelValues(label))
			}
			for round := 0; round < 2; round++ {
				err := job.run(context.Background())
				if (err != nil) != (round == 0 && failure == "scan") {
					t.Fatalf("round %d: err = %v", round, err)
				}
				label := result
				if round == 1 {
					label = "success"
				}
				before[label]++
				if got := testutil.ToFloat64(metrics.PaymentReconcileFailed); got != float64(1-round) {
					t.Fatalf("round %d: failed = %v", round, got)
				}
				for label, want := range before {
					if got := testutil.ToFloat64(metrics.PaymentReconcileRuns.WithLabelValues(label)); got != want {
						t.Fatalf("round %d: %s = %v, want %v", round, label, got, want)
					}
				}
				if round == 0 && (repo.order.Status != PaymentOrderStatusPending || issuer.delegate.issued != 0) {
					t.Fatal("failed round must stay pending without issuing assets")
				}
				repo.err, provider.err = nil, nil
			}
			if repo.order.Status != PaymentOrderStatusPaid || issuer.delegate.issued != 1 {
				t.Fatal("recovery must mark paid and issue exactly once")
			}
		})
	}
}

type interruptedPaymentProvider struct {
	*statusPaymentProvider
	cancel context.CancelFunc
}

func (p *interruptedPaymentProvider) QueryOrder(ctx context.Context, _ *PaymentOrder) (*PaymentProviderStatus, error) {
	p.cancel()
	return nil, ctx.Err()
}

func TestPaymentReconcileJobInterruptedScanPreservesMetrics(t *testing.T) {
	for _, phase := range []string{"before_scan", "scan", "query", "empty_scan"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo := &failingPaymentScanRepo{memoryPaymentRepo: &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-CANCEL")}}
			var provider PaymentProvider = &statusPaymentProvider{}
			switch phase {
			case "before_scan":
				cancel()
				repo.beforeList = func() { t.Error("canceled job must not start a scan") }
			case "scan":
				repo.beforeList, repo.err = cancel, context.Canceled
			case "query":
				provider = &interruptedPaymentProvider{statusPaymentProvider: &statusPaymentProvider{}, cancel: cancel}
			case "empty_scan":
				repo.order, repo.beforeList = nil, cancel
			}
			issuer := &countingPaymentIssuer{}
			job := NewPaymentReconcileJob(NewPaymentUsecase(repo, provider, issuer), time.Minute)
			before := map[string]float64{}
			for _, label := range []string{"success", "partial", "error"} {
				before[label] = testutil.ToFloat64(metrics.PaymentReconcileRuns.WithLabelValues(label))
			}
			// An interrupted empty scan must not clear an earlier failure either.
			failed := 0.0
			if phase == "empty_scan" {
				failed = 1
			}
			previous := testutil.ToFloat64(metrics.PaymentReconcileFailed)
			t.Cleanup(func() { metrics.PaymentReconcileFailed.Set(previous) })
			metrics.PaymentReconcileFailed.Set(failed)
			if err := job.run(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
			for label, want := range before {
				if got := testutil.ToFloat64(metrics.PaymentReconcileRuns.WithLabelValues(label)); got != want {
					t.Errorf("%s = %v, want unchanged %v", label, got, want)
				}
			}
			if got := testutil.ToFloat64(metrics.PaymentReconcileFailed); got != failed {
				t.Errorf("failed = %v, want unchanged %v", got, failed)
			}
			if issuer.issued != 0 {
				t.Fatal("interrupted scan issued assets")
			}
		})
	}
}

func TestPaymentReconcileJobHealthyPendingAndEmptyScan(t *testing.T) {
	repo := &memoryPaymentRepo{order: pendingAlipayBalanceOrder("PAY-PENDING")}
	issuer := &countingPaymentIssuer{}
	job := NewPaymentReconcileJob(NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{}}, issuer), time.Minute)
	before := testutil.ToFloat64(metrics.PaymentReconcileRuns.WithLabelValues("success"))
	for round := 0; round < 2; round++ {
		metrics.PaymentReconcileFailed.Set(1)
		if err := job.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if testutil.ToFloat64(metrics.PaymentReconcileFailed) != 0 || issuer.issued != 0 {
			t.Fatal("healthy pending/empty scan must clear failure without issuing assets")
		}
		repo.order = nil
	}
	if got := testutil.ToFloat64(metrics.PaymentReconcileRuns.WithLabelValues("success")); got != before+2 {
		t.Fatalf("success = %v, want %v", got, before+2)
	}
}
