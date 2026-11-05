package metrics

import (
	"bytes"
	"github.com/prometheus/common/expfmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingGrossProfitSeparatesZeroAndNegativeQuota(t *testing.T) {
	const family = "margin_test"
	histogram := BillingLedgerGrossProfit.WithLabelValues(family)
	t.Cleanup(func() { BillingLedgerGrossProfit.DeleteLabelValues(family) })
	histogram.Observe(0)
	histogram.Observe(-1)
	metric := &dto.Metric{}
	require.NoError(t, histogram.(prometheus.Metric).Write(metric))
	bounds := map[float64]uint64{}
	for _, bucket := range metric.GetHistogram().Bucket {
		bounds[bucket.GetUpperBound()] = bucket.GetCumulativeCount()
	}
	require.Contains(t, bounds, float64(-1), "integer quota margins need an exact strictly-negative bucket")
	assert.Equal(t, uint64(1), bounds[-1])
	assert.Equal(t, uint64(2), bounds[0])
}

func TestBillingMarginMetricsHaveDistinctOpenMetricsFamilies(t *testing.T) {
	const family = "margin_openmetrics_test"
	BillingLedgerGrossProfit.WithLabelValues(family).Observe(0)
	BillingLedgerGrossProfitTotal.WithLabelValues(family).Add(0)
	BillingLedgerGrossLossTotal.WithLabelValues(family).Add(0)
	t.Cleanup(func() {
		BillingLedgerGrossProfit.DeleteLabelValues(family)
		BillingLedgerGrossProfitTotal.DeleteLabelValues(family)
		BillingLedgerGrossLossTotal.DeleteLabelValues(family)
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(BillingLedgerGrossProfit, BillingLedgerGrossProfitTotal, BillingLedgerGrossLossTotal)
	families, err := registry.Gather()
	require.NoError(t, err)
	var output bytes.Buffer
	for _, family := range families {
		_, err = expfmt.MetricFamilyToOpenMetrics(&output, family)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, strings.Count(output.String(), "# TYPE micro_one_api_billing_ledger_gross_profit_quota "), "OpenMetrics removes _total from counter family names")
}
