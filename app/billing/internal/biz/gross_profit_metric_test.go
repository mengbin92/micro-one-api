package biz

import (
	"context"
	"errors"
	"testing"

	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func grossProfitFixture(t *testing.T) (*BillingUsecase, *mockLedgerRepo, *mockReservationRepo, *prometheus.Registry) {
	t.Helper()
	t.Setenv("BILLING_CACHE_CREATION_MODE", "charge")
	account := &mockAccountRepo{account: &Account{UserID: "42", Balance: 1000000, FrozenAmount: 1000, Group: "default"}}
	reservations := &mockReservationRepo{reservations: map[string]*Reservation{"r": {ReservationID: "r", UserID: "42", Amount: 1000, BalanceAmount: 1000, Model: "deepseek-model", Status: ReservationStatusReserved}}}
	ledger := &mockLedgerRepo{}
	uc := NewBillingUsecaseWithPricing(account, reservations, ledger, nil, PricingConfig{ModelPrices: map[string]ModelPrice{"deepseek-model": {InputPrice: .001}}})
	registry := prometheus.NewRegistry()
	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_margin", Help: "test margin", Buckets: []float64{-100, -1, 0, 100}}, []string{"provider_family"})
	profit := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_profit_total", Help: "test profit"}, []string{"provider_family"})
	loss := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_loss_total", Help: "test loss"}, []string{"provider_family"})
	registry.MustRegister(histogram, profit, loss)
	uc.SetGrossProfitMetrics(histogram, profit, loss)
	return uc, ledger, reservations, registry
}

func grossProfitSamples(t *testing.T, registry *prometheus.Registry) (count uint64, sum, profit, loss float64, counterSeries int) {
	t.Helper()
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		for _, metric := range family.Metric {
			assert.Equal(t, "deepseek", labelValue(metric, "provider_family"))
			switch family.GetName() {
			case "test_margin":
				count += metric.GetHistogram().GetSampleCount()
				sum += metric.GetHistogram().GetSampleSum()
			case "test_profit_total":
				profit += metric.GetCounter().GetValue()
				counterSeries++
			case "test_loss_total":
				loss += metric.GetCounter().GetValue()
				counterSeries++
			}
		}
	}
	return
}

func TestCommitQuota_EmitsGrossProfitMetric(t *testing.T) {
	for _, path := range []string{"legacy", "transaction"} {
		for _, margin := range []int64{1, 0, -1} {
			t.Run(path+"/"+map[int64]string{1: "profit", 0: "zero", -1: "loss"}[margin], func(t *testing.T) {
				uc, ledger, _, registry := grossProfitFixture(t)
				if path == "transaction" {
					uc.SetTxRunner(&mockTxRunner{})
				}
				usage := LedgerUsage{PromptTokens: 100, UpstreamCost: 1000 - margin}
				actual, _, err := uc.CommitQuotaWithUsage(context.Background(), "r", 100, true, usage)
				require.NoError(t, err)
				assert.Equal(t, int64(1000), actual)
				// A duplicate delivery must not count the request or its costs again.
				_, _, err = uc.CommitQuotaWithUsage(context.Background(), "r", 100, true, usage)
				require.NoError(t, err)
				require.Len(t, ledger.ledgers, 1)
				count, sum, profit, loss, series := grossProfitSamples(t, registry)
				assert.Equal(t, uint64(1), count)
				assert.Equal(t, float64(margin), sum)
				assert.Equal(t, float64(maxInt64(margin, 0)), profit)
				assert.Equal(t, float64(maxInt64(-margin, 0)), loss)
				assert.Equal(t, 2, series, "both counter sides must exist, including when the first margin is zero")
			})
		}
	}
}

// A failure after the callback models database COMMIT rejection, which must
// not emit a margin even though all statements in the callback succeeded.
type grossProfitCommitRunner struct {
	beforeCommit func()
	commitErr    error
}

func (r grossProfitCommitRunner) RunInTx(ctx context.Context, fn func(context.Context, subscriptionbiz.Tx) error) error {
	if err := fn(ctx, &mockTx{}); err != nil {
		return err
	}
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	return r.commitErr
}

type grossProfitFailingLedger struct {
	*mockLedgerRepo
	failAt, calls int
}

func (r *grossProfitFailingLedger) CreateLedger(ctx context.Context, ledger *Ledger) error {
	r.calls++
	if r.calls == r.failAt {
		return errors.New("ledger rejected")
	}
	return r.mockLedgerRepo.CreateLedger(ctx, ledger)
}

func (r *grossProfitFailingLedger) CreateLedgerInTx(ctx context.Context, _ subscriptionbiz.Tx, ledger *Ledger) error {
	return r.CreateLedger(ctx, ledger)
}

func grossProfitSplitReservation(uc *BillingUsecase, reservations *mockReservationRepo) {
	reservations.reservations["r"].SubscriptionID = 7
	reservations.reservations["r"].SubscriptionAmountUSD = .05
	uc.SetSubscriptionPrimatives(&mockSubscriptionPrimitives{})
}

func TestCommitQuota_GrossProfitIgnoresUnsettledRequests(t *testing.T) {
	for _, mode := range []string{"ledger failure", "transaction rollback", "second split ledger failure", "release"} {
		t.Run(mode, func(t *testing.T) {
			uc, ledger, reservations, registry := grossProfitFixture(t)
			success := true
			switch mode {
			case "ledger failure":
				uc.SetLedgerRepo(&grossProfitFailingLedger{mockLedgerRepo: ledger, failAt: 1})
			case "transaction rollback":
				uc.SetTxRunner(grossProfitCommitRunner{commitErr: errors.New("commit rejected")})
			case "second split ledger failure":
				grossProfitSplitReservation(uc, reservations)
				uc.SetTxRunner(&mockTxRunner{})
				uc.SetLedgerRepo(&grossProfitFailingLedger{mockLedgerRepo: ledger, failAt: 2})
			case "release":
				success = false
			}
			_, _, err := uc.CommitQuotaWithUsage(context.Background(), "r", 100, success, LedgerUsage{PromptTokens: 100, UpstreamCost: 1001})
			if success {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			count, _, profit, loss, series := grossProfitSamples(t, registry)
			assert.Zero(t, count, "unsettled requests must not contribute a margin observation")
			assert.Zero(t, profit)
			assert.Zero(t, loss)
			assert.Zero(t, series)
		})
	}
}

func TestCommitQuota_GrossProfitSplitCountsOnceAfterCommit(t *testing.T) {
	uc, ledger, reservations, registry := grossProfitFixture(t)
	grossProfitSplitReservation(uc, reservations)
	uc.SetTxRunner(grossProfitCommitRunner{beforeCommit: func() {
		count, _, _, _, series := grossProfitSamples(t, registry)
		assert.Zero(t, count, "the transaction has not committed yet")
		assert.Zero(t, series)
	}})
	usage := LedgerUsage{PromptTokens: 100, UpstreamCost: 1001}
	_, _, err := uc.CommitQuotaWithUsage(context.Background(), "r", 100, true, usage)
	require.NoError(t, err)
	require.Len(t, ledger.ledgers, 2)
	assert.Equal(t, int64(1001), ledger.ledgers[0].UpstreamCost+ledger.ledgers[1].UpstreamCost)
	// Idempotent re-entry executes the runner but must not emit an observation.
	uc.SetTxRunner(&mockTxRunner{})
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), "r", 100, true, usage)
	require.NoError(t, err)
	count, sum, profit, loss, series := grossProfitSamples(t, registry)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, float64(-1), sum)
	assert.Zero(t, profit)
	assert.Equal(t, float64(1), loss)
	assert.Equal(t, 2, series)
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// The first callback loses its database COMMIT; before the replay, another
// writer commits the same reservation. The fake retains that terminal state.
type grossProfitReplayedRunner struct{}

func (grossProfitReplayedRunner) RunInTx(ctx context.Context, fn func(context.Context, subscriptionbiz.Tx) error) error {
	if err := fn(ctx, &mockTx{}); err != nil {
		return err
	}
	return fn(ctx, &mockTx{})
}

func TestCommitQuota_GrossProfitReplayDoesNotCountAnotherWriter(t *testing.T) {
	uc, _, _, registry := grossProfitFixture(t)
	uc.SetTxRunner(grossProfitReplayedRunner{})
	_, _, err := uc.CommitQuotaWithUsage(context.Background(), "r", 100, true, LedgerUsage{PromptTokens: 100, UpstreamCost: 1001})
	require.NoError(t, err)
	count, _, _, _, series := grossProfitSamples(t, registry)
	assert.Zero(t, count, "the successful replay only read another writer's settlement")
	assert.Zero(t, series)
}
