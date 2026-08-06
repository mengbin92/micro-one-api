package biz

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommitQuota_EmitsGrossProfitMetric guards the v0.17 roadmap P1.1
// charge-mode margin signal: every commit must record per-request gross
// profit (charged quota minus upstream cost) on the
// micro_one_api_billing_ledger_gross_profit_quota histogram so the
// NegativeGrossMargin Prometheus alert can fire.
func TestCommitQuota_EmitsGrossProfitMetric(t *testing.T) {
	t.Setenv("BILLING_CACHE_CREATION_MODE", "charge")
	account := &Account{UserID: "u-gross", Balance: 1_000_000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelPrices: map[string]ModelPrice{
			"glm-5.2": {InputPrice: 0.001, OutputPrice: 0.002},
		},
		UpstreamPrices: map[string]ModelPrice{
			"glm-5.2": {InputPrice: 0.0005, OutputPrice: 0.001},
		},
	})

	reservation, err := uc.ReserveQuota(context.Background(), "u-gross", "req-gross", 1000, "glm-5.2", "ch1", 0)
	require.NoError(t, err)
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 1000, true, LedgerUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
	})
	require.NoError(t, err)

	// upstream cost = 100*0.0005 + 50*0.001 = 0.1 USD = 1000 quota
	// actual cost   = 100*0.001  + 50*0.002 = 0.2 USD = 2000 quota
	// gross profit  = 2000 - 1000 = 1000 quota
	const expectedGross = float64(1000)

	var (
		found  bool
		series = 0
	)
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "micro_one_api_billing_ledger_gross_profit_quota" {
			continue
		}
		for _, m := range mf.GetMetric() {
			series++
			if labelValue(m, "provider_family") != "zhipu" {
				continue
			}
			found = true
			h := m.GetHistogram()
			assert.GreaterOrEqual(t, h.GetSampleCount(), uint64(1), "gross profit must be observed at least once")
			assert.GreaterOrEqual(t, h.GetSampleSum(), expectedGross,
				"zhipu gross profit observations must include the 1000-quota commit")
		}
	}
	assert.True(t, found, "gross profit metric must exist for provider_family=zhipu")
	assert.GreaterOrEqual(t, series, 1, "histogram must expose at least one labeled series")
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}
