package billing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func TestServiceTierConfigurationRequiresExplicitAtomicTariffs(t *testing.T) {
	price := `{"input_price":0,"output_price":0,"cache_read_price":0}`
	valid := `{"version":1,"enabled":true,"source_kind":"channel","source_id":11,"model":"gpt-5.1","prices":{"default":` + price + `,"priority":` + price + `,"flex":` + price + `}}`
	c, err := ParseServiceTierConfig(valid)
	require.NoError(t, err)
	require.True(t, c.Enabled)
	for _, raw := range []string{"null", "[]", valid + "{}", strings.Replace(valid, `"input_price":0,`, "", 1), strings.Replace(valid, `"output_price":0`, `"output_price":null`, 1), strings.Replace(valid, `"cache_read_price":0`, `"cache_read_price":-1`, 1), strings.Replace(valid, `"priority":`, `"scale":`, 1), strings.Replace(valid, `"output_price":0`, `"output_typo":0`, 1), strings.Replace(valid, `"source_id":11`, `"source_id":0`, 1)} {
		_, err = ParseServiceTierConfig(raw)
		require.Error(t, err, raw)
	}
	for _, raw := range []string{`{}`, `{"version":1,"enabled":false}`, strings.Replace(valid, `"enabled":true`, `"enabled":false`, 1)} {
		require.NoError(t, ValidateServiceTierConfig(raw))
	}
}

func TestMaxTierPriceBoundsEveryBucketIndependently(t *testing.T) {
	pointer := func(v float64) *float64 { return &v }
	p := MaxTierPrice(map[string]ModelPrice{"default": {InputPrice: 2, OutputPrice: 1, CacheReadPrice: pointer(7)}, "priority": {InputPrice: 5, OutputPrice: 3, CacheReadPrice: pointer(4), CacheCreation5mPrice: pointer(9)}, "flex": {InputPrice: 1, OutputPrice: 6, CacheReadPrice: pointer(1), CacheCreation1hPrice: pointer(11)}})
	require.EqualValues(t, 5, p.InputPrice)
	require.EqualValues(t, 6, p.OutputPrice)
	require.EqualValues(t, 7, *p.CacheReadPrice)
	require.EqualValues(t, 9, *p.CacheCreation5mPrice)
	require.EqualValues(t, 11, *p.CacheCreation1hPrice)
}

func TestLegacySettlementJSONOmitsAbsentServiceTierFields(t *testing.T) {
	raw, err := jsonx.Marshal(LedgerUsage{})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "ActualServiceTier")
	require.NotContains(t, string(raw), "ServiceTierStatus")
	require.NotContains(t, string(raw), "FrozenServiceTier")
	// A real observation is part of the immutable settlement digest.
	observed, err := jsonx.Marshal(LedgerUsage{ActualServiceTier: "priority", ServiceTierStatus: "reported"})
	require.NoError(t, err)
	require.Contains(t, string(observed), `"ActualServiceTier":"priority"`)
}
