package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/billing"
	"micro-one-api/pkg/jsonx"
)

func publicTimePricingFixture() billing.ServiceTierConfig {
	prices := map[string]billing.ModelPrice{}
	peak := map[string]billing.ModelPrice{}
	for _, tier := range []string{"default", "priority", "flex"} {
		prices[tier] = billing.ModelPrice{InputPrice: .0000002, OutputPrice: .0000004, CacheReadPrice: new(.00000002), CacheCreation5mPrice: new(0.0), CacheCreation1hPrice: new(0.0)}
		peak[tier] = billing.ModelPrice{InputPrice: .0000004, OutputPrice: .0000008, CacheReadPrice: new(.00000004), CacheCreation5mPrice: new(0.0), CacheCreation1hPrice: new(0.0)}
	}
	config := billing.ServiceTierConfig{Version: 3, Enabled: true, SourceKind: "channel", SourceID: 11, Model: "deepseek-flash", Prices: prices, TimePricing: &billing.TimePricingConfig{Timezone: "Asia/Shanghai", CalendarValidThrough: "2026-12-31", Holidays: []string{}, PeakWindows: []billing.PricingTimeWindow{{Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 540, EndMinute: 1080}}, PeakPrices: peak}}
	return config
}

func TestReadonlyPricingIncludesEnabledTimePricing(t *testing.T) {
	config := publicTimePricingFixture()
	config.UpstreamPrices = config.Prices
	config.LongContext = &billing.LongContextConfig{ThresholdTokens: 128000, Policy: billing.LongContextPolicy, Prices: config.TimePricing.PeakPrices, UpstreamPrices: config.Prices}
	config.TimePricing.PeakUpstreamPrices = config.Prices
	config.TimePricing.PeakLongContextPrices = config.TimePricing.PeakPrices
	config.TimePricing.PeakLongContextUpstreamPrices = config.Prices
	raw, err := jsonx.Marshal(config)
	require.NoError(t, err)
	_, err = billing.ParseServiceTierConfig(string(raw))
	require.NoError(t, err)
	srv := newAdminHTTPOptionTestServer(&adminHTTPSystemOptionsStore{values: map[string]string{
		"ModelPrice":         `{"deepseek-flash":{"input_price":0.0000002,"output_price":0.0000004}}`,
		"ServiceTierPricing": string(raw),
	}})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"time_pricing"`, "configured busy/idle prices must reach the pricing page")
	require.Contains(t, rec.Body.String(), `"timezone":"Asia/Shanghai"`)
	require.Contains(t, rec.Body.String(), `"threshold_tokens":128000`)
	require.Contains(t, rec.Body.String(), `"input_price":0.2`)
	require.Contains(t, rec.Body.String(), `"output_price":0.8`)
	for _, private := range []string{"upstream_prices", "peak_upstream_prices", "peak_long_context_upstream_prices", "source_id", "source_kind"} {
		require.NotContains(t, rec.Body.String(), private)
	}
}

func TestReadonlyPricingDisabledAndInvalidTimePricing(t *testing.T) {
	for _, raw := range []string{``, `{"enabled":false}`, `{"enabled":true}`, `{`} {
		t.Run(raw, func(t *testing.T) {
			srv := newAdminHTTPOptionTestServer(&adminHTTPSystemOptionsStore{values: map[string]string{"ServiceTierPricing": raw}})
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
			if raw == "" || raw == `{"enabled":false}` {
				require.Equal(t, http.StatusOK, rec.Code)
				require.NotContains(t, rec.Body.String(), `"time_pricing"`)
			} else {
				require.Equal(t, http.StatusInternalServerError, rec.Code)
				require.NotContains(t, rec.Body.String(), `"prices"`)
			}
		})
	}
}

func TestReadonlyPricingTierOnlyModelStillRequiresPublicCatalog(t *testing.T) {
	raw, err := jsonx.Marshal(publicTimePricingFixture())
	require.NoError(t, err)
	store := &adminHTTPSystemOptionsStore{values: map[string]string{"ServiceTierPricing": string(raw)}}
	channel := &guardedPricingChannelClient{adminHTTPModelChannelClient: adminHTTPModelChannelClient{models: []*channelv1.ModelSummary{{ModelId: "deepseek-flash", Status: 1, IsPublic: true}}}}
	svc := service.NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	for _, public := range []bool{true, false} {
		channel.models[0].IsPublic = public
		rec := httptest.NewRecorder()
		handleReadonlyPricing(rec, httptest.NewRequest(http.MethodGet, "/api/pricing", nil), svc)
		require.Equal(t, http.StatusOK, rec.Code)
		if public {
			require.Contains(t, rec.Body.String(), `"time_pricing"`)
		} else {
			require.NotContains(t, rec.Body.String(), "deepseek-flash")
		}
	}
}

func TestReadonlyPricingBaseTierPreservesUnpricedCacheCreation(t *testing.T) {
	config := publicTimePricingFixture()
	config.Version, config.TimePricing = 1, nil
	for tier, price := range config.Prices {
		price.CacheCreation5mPrice, price.CacheCreation1hPrice = nil, nil
		config.Prices[tier] = price
	}
	raw, err := jsonx.Marshal(config)
	require.NoError(t, err)
	srv := newAdminHTTPOptionTestServer(&adminHTTPSystemOptionsStore{values: map[string]string{"ServiceTierPricing": string(raw)}})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var response struct {
		Data struct {
			Prices []map[string]jsonx.RawMessage `json:"prices"`
		} `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Data.Prices, 1)
	require.NotContains(t, response.Data.Prices[0], "cache_creation_5m_price")
	require.NotContains(t, response.Data.Prices[0], "cache_creation_1h_price")
	require.JSONEq(t, `true`, string(response.Data.Prices[0]["cache_creation_unpriced"]))
}

func TestReadonlyPricingTierReplacesCaseAliases(t *testing.T) {
	config := publicTimePricingFixture()
	config.Model = "DeepSeek-Flash"
	raw, err := jsonx.Marshal(config)
	require.NoError(t, err)
	store := &adminHTTPSystemOptionsStore{values: map[string]string{
		"ModelPrice":         `{"DEEPSEEK-FLASH":{"input_price":0.000001},"deepseek-flash":{"input_price":0.000002}}`,
		"ServiceTierPricing": string(raw),
	}}
	channel := &guardedPricingChannelClient{adminHTTPModelChannelClient: adminHTTPModelChannelClient{models: []*channelv1.ModelSummary{{ModelId: "deepseek-flash", Status: 1, IsPublic: true}}}}
	svc := service.NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	rec := httptest.NewRecorder()
	handleReadonlyPricing(rec, httptest.NewRequest(http.MethodGet, "/api/pricing", nil), svc)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"time_pricing"`)
	var response struct {
		Data struct {
			Prices []readonlyPricingRow `json:"prices"`
		} `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Data.Prices, 1)
}
