package biz

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

type modelPricingStore struct {
	SystemOptionsRepo
	model string
	row   string
	calls int
}

func (s *modelPricingStore) MutateModelPrice(_ context.Context, model string, fn func(string) (string, error)) error {
	s.calls++
	s.model = model
	value, err := fn(s.row)
	if err == nil {
		s.row = value
	}
	return err
}

func TestPublishModelPricingPublishesOnceAndKeepsOtherPriceFields(t *testing.T) {
	store := &modelPricingStore{}
	uc := NewSystemOptionsUsecase(store)
	price := ModelPricing{ModelID: " New-Model ", InputPrice: 0.3, OutputPrice: 1.2, CacheReadPrice: 0.06}
	require.NoError(t, uc.PublishModelPricing(context.Background(), nil, price))
	require.Equal(t, "new-model", store.model)
	require.JSONEq(t, `{"input_price":0.0000003,"output_price":0.0000012,"cache_read_price":0.00000006}`, store.row)
	store.row = `{"input_price":0.000009,"output_price":0.000012,"cache_creation_5m_price":0,"cache_creation_1h_price":0.000004}`
	unchanged := store.row
	require.NoError(t, uc.PublishModelPricing(context.Background(), &price, price))
	require.Equal(t, unchanged, store.row, "metadata-only edits must preserve independently managed sale prices")
	updated := price
	updated.InputPrice = 0.4
	updated.CacheReadPrice = 0
	require.NoError(t, uc.PublishModelPricing(context.Background(), &price, updated))
	require.JSONEq(t, `{"input_price":0.0000004,"output_price":0.0000012,"cache_creation_5m_price":0,"cache_creation_1h_price":0.000004}`, store.row)
	var row map[string]jsonx.RawMessage
	require.NoError(t, jsonx.Unmarshal([]byte(store.row), &row))
	require.NotContains(t, row, "cache_read_price")
}

func TestPublishModelPricingBackfillsUnpublishedAndSkipsUnconfigured(t *testing.T) {
	store := &modelPricingStore{}
	uc := NewSystemOptionsUsecase(store)
	unconfigured := ModelPricing{ModelID: "m"}
	require.NoError(t, uc.PublishModelPricing(context.Background(), nil, unconfigured))
	require.NoError(t, uc.PublishModelPricing(context.Background(), &unconfigured, unconfigured))
	require.Zero(t, store.calls)
	price := ModelPricing{ModelID: "m", InputPrice: 1, OutputPrice: 2}
	require.NoError(t, uc.PublishModelPricing(context.Background(), &price, price))
	require.JSONEq(t, `{"input_price":0.000001,"output_price":0.000002}`, store.row)
	require.NoError(t, uc.PublishModelPricing(context.Background(), &price, unconfigured))
	require.JSONEq(t, `{"input_price":0,"output_price":0,"cache_read_price":0}`, store.row, "an explicit free cache price keeps billing from dropping the zero tariff and reviving legacy ratios")
}

func TestPublishModelPricingValidatesBeforeWritingAndRequiresStorage(t *testing.T) {
	store := &modelPricingStore{}
	uc := NewSystemOptionsUsecase(store)
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		for _, price := range []ModelPricing{{ModelID: "m", InputPrice: value}, {ModelID: "m", OutputPrice: value}, {ModelID: "m", CacheReadPrice: value}} {
			require.Error(t, uc.PublishModelPricing(context.Background(), nil, price))
		}
	}
	require.Zero(t, store.calls)
	require.Error(t, ValidateModelPricing(ModelPricing{ModelID: " "}))
	require.ErrorIs(t, (*SystemOptionsUsecase)(nil).PublishModelPricing(context.Background(), nil, ModelPricing{ModelID: "m", InputPrice: 1}), authorization.ErrWriteStorageUnavailable)
}

func TestPublishModelPricingExplicitZeroOverridesHiddenPreviousPrices(t *testing.T) {
	store := &modelPricingStore{row: `{"input_price":0.000003,"output_price":0.000012}`}
	uc := NewSystemOptionsUsecase(store)
	redacted := ModelPricing{ModelID: "m"}
	command := ModelPricing{ModelID: "m", ForcePublish: true}
	require.NoError(t, uc.PublishModelPricing(context.Background(), &redacted, command))
	require.Equal(t, 1, store.calls)
	require.JSONEq(t, `{"input_price":0,"output_price":0,"cache_read_price":0}`, store.row, "redacted previous fields must not suppress an explicit authorized free-price command")
}
