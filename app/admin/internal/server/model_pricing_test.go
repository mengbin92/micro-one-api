package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type modelPricingChannelClient struct {
	guardedPricingChannelClient
}

func (c *modelPricingChannelClient) UpdateModel(_ context.Context, req *channelv1.UpdateModelRequest, _ ...grpc.CallOption) (*channelv1.UpdateModelResponse, error) {
	c.updatedModel = req
	c.models[0].PricingInput = req.PricingInput
	c.models[0].PricingOutput = req.PricingOutput
	c.models[0].PricingCacheRead = req.PricingCacheRead
	return &channelv1.UpdateModelResponse{Success: true}, nil
}

func TestModelPriceEditPublishesReadonlyPricing(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	store := data.NewSystemOptionsRepoWithDriver(sqlDB, "sqlite")
	require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (519,'new-model','New model')").Error)
	channel := &modelPricingChannelClient{guardedPricingChannelClient: guardedPricingChannelClient{
		adminHTTPModelChannelClient: adminHTTPModelChannelClient{
			models:      []*channelv1.ModelSummary{{Id: 519, ModelId: "new-model", Status: 1, IsPublic: true}},
			modelDetail: &channelv1.GetModelResponse{Model: &channelv1.ModelInfo{Id: 519, ModelId: "new-model", Status: 1, IsPublic: true}},
		},
	}}
	svc := service.NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	edit := httptest.NewRecorder()
	handleModels(edit, httptest.NewRequest(http.MethodPut, "/api/admin/models", strings.NewReader(`{"model_pk":519,"is_public":true,"pricing_input":0.3,"pricing_output":1.2,"pricing_cache_read":0.06}`)), svc)
	require.Equal(t, http.StatusOK, edit.Code, edit.Body.String())
	require.True(t, channel.models[0].GetPricingInput() > 0, "model registry must contain the saved price")

	public := httptest.NewRecorder()
	handleReadonlyPricing(public, httptest.NewRequest(http.MethodGet, "/api/pricing", nil), svc)
	require.Equal(t, http.StatusOK, public.Code, public.Body.String())
	var response struct {
		Data struct {
			Prices []readonlyPricingRow `json:"prices"`
		} `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(public.Body.Bytes(), &response))
	require.Len(t, response.Data.Prices, 1, "saving prices in the model editor must publish them without a second save on admin/pricing")
	require.Equal(t, "new-model", response.Data.Prices[0].Model)
	require.Equal(t, 0.3, *response.Data.Prices[0].InputPrice)
	require.Equal(t, 1.2, *response.Data.Prices[0].OutputPrice)
	require.Equal(t, 0.06, *response.Data.Prices[0].CacheReadPrice)
	stored, err := store.Get(context.Background(), "ModelPrice")
	require.NoError(t, err)
	require.JSONEq(t, `{"new-model":{"input_price":0.0000003,"output_price":0.0000012,"cache_read_price":0.00000006}}`, stored, "public prices must come from actual billing configuration")
}

type unavailableModelPricingStore struct{ adminHTTPSystemOptionsStore }

func (s *unavailableModelPricingStore) MutateModelPrice(context.Context, string, func(string) (string, error)) error {
	return errors.New("private database connection details")
}

func TestModelPricePublishFailureExplainsPartialSaveWithoutLeakingStorage(t *testing.T) {
	store := &unavailableModelPricingStore{}
	channel := &adminHTTPModelChannelClient{modelDetail: &channelv1.GetModelResponse{Model: &channelv1.ModelInfo{Id: 519, ModelId: "new-model"}}}
	svc := service.NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	rec := httptest.NewRecorder()
	handleModels(rec, httptest.NewRequest(http.MethodPut, "/api/admin/models", strings.NewReader(`{"model_pk":519,"pricing_input":0.3}`)), svc)
	require.NotEqual(t, http.StatusOK, rec.Code)
	require.NotNil(t, channel.updatedModel)
	require.Contains(t, rec.Body.String(), "model saved, but publishing its billing price failed; retry in pricing management")
	require.NotContains(t, rec.Body.String(), "private database connection details")
}
