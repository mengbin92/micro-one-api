package service

import (
	"context"
	"errors"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type modelPriceServiceStore struct {
	biz.SystemOptionsRepo
	row    string
	writes int
	err    error
}

func (s *modelPriceServiceStore) MutateModelPrice(_ context.Context, _ string, fn func(string) (string, error)) error {
	s.writes++
	if s.err != nil {
		return s.err
	}
	value, err := fn(s.row)
	if err == nil {
		s.row = value
	}
	return err
}

type modelPriceServiceChannel struct {
	channelv1.ChannelServiceClient
	detail                 *channelv1.ModelInfo
	gets, creates, updates int
	rejected               bool
}

func (c *modelPriceServiceChannel) GetModel(context.Context, *channelv1.GetModelRequest, ...grpc.CallOption) (*channelv1.GetModelResponse, error) {
	c.gets++
	return &channelv1.GetModelResponse{Model: c.detail}, nil
}

func (c *modelPriceServiceChannel) CreateModel(context.Context, *channelv1.CreateModelRequest, ...grpc.CallOption) (*channelv1.CreateModelResponse, error) {
	c.creates++
	return &channelv1.CreateModelResponse{Success: !c.rejected, ModelPk: 519}, nil
}

func (c *modelPriceServiceChannel) UpdateModel(context.Context, *channelv1.UpdateModelRequest, ...grpc.CallOption) (*channelv1.UpdateModelResponse, error) {
	c.updates++
	return &channelv1.UpdateModelResponse{Success: !c.rejected}, nil
}

func TestCreateModelPublishesPricesOnlyAfterSuccessfulCreation(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "created", true: "rejected"}[rejected], func(t *testing.T) {
			store := &modelPriceServiceStore{}
			channel := &modelPriceServiceChannel{rejected: rejected}
			svc := NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
			resp, err := svc.CreateModel(context.Background(), &channelv1.CreateModelRequest{ModelId: "New-Model", PricingInput: 0.3, PricingOutput: 1.2})
			require.NoError(t, err)
			require.Equal(t, !rejected, resp.GetSuccess())
			if rejected {
				require.Zero(t, store.writes)
			} else {
				require.JSONEq(t, `{"input_price":0.0000003,"output_price":0.0000012}`, store.row)
			}
		})
	}
}

func TestUpdateModelPreservesProtectedAndIndependentlyManagedPrices(t *testing.T) {
	store := &modelPriceServiceStore{row: `{"input_price":0.000009,"output_price":0.000012}`}
	channel := &modelPriceServiceChannel{detail: &channelv1.ModelInfo{Id: 519, ModelId: "new-model", PricingInput: 0.3, PricingOutput: 1.2, PriceFieldsVisible: true}}
	svc := NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	resp, err := svc.UpdateModel(context.Background(), &channelv1.UpdateModelRequest{ModelPk: 519, PricingInput: 0.3, PricingOutput: 1.2, DisplayName: "Renamed"})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess())
	require.JSONEq(t, `{"input_price":0.000009,"output_price":0.000012}`, store.row)
	store.writes = 0
	channel.gets = 0
	resp, err = svc.UpdateModel(context.Background(), &channelv1.UpdateModelRequest{ModelPk: 519, PreservePricing: true})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess())
	require.Zero(t, store.writes)
	require.Zero(t, channel.gets, "protected prices must not be read or republished")
}

func TestUpdateModelCannotTreatRedactedPricesAsUnchangedZeroPrices(t *testing.T) {
	store := &modelPriceServiceStore{row: `{"input_price":0.000009,"output_price":0.000012}`}
	channel := &modelPriceServiceChannel{detail: &channelv1.ModelInfo{Id: 519, ModelId: "new-model", PriceFieldsVisible: false}}
	svc := NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	resp, err := svc.UpdateModel(context.Background(), &channelv1.UpdateModelRequest{ModelPk: 519})
	require.NoError(t, err)
	require.True(t, resp.GetSuccess())
	require.JSONEq(t, `{"input_price":0,"output_price":0,"cache_read_price":0}`, store.row)
}

func TestModelPricingValidationPrecedesRegistryWrite(t *testing.T) {
	store := &modelPriceServiceStore{}
	channel := &modelPriceServiceChannel{detail: &channelv1.ModelInfo{Id: 519, ModelId: "new-model"}}
	svc := NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	_, err := svc.CreateModel(context.Background(), &channelv1.CreateModelRequest{ModelId: "new-model", PricingInput: -1})
	require.ErrorIs(t, err, biz.ErrModelPricingInvalid)
	_, err = svc.UpdateModel(context.Background(), &channelv1.UpdateModelRequest{ModelPk: 519, PricingOutput: -1})
	require.ErrorIs(t, err, biz.ErrModelPricingInvalid)
	require.Zero(t, channel.creates)
	require.Zero(t, channel.updates)
	require.Zero(t, store.writes)
}

func TestModelPricingPublishFailureIsNotReportedAsSuccessfulSave(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	store := &modelPriceServiceStore{err: storageErr}
	channel := &modelPriceServiceChannel{detail: &channelv1.ModelInfo{Id: 519, ModelId: "new-model"}}
	svc := NewAdminService(nil, nil, channel, biz.NewSystemOptionsUsecase(store))
	resp, err := svc.UpdateModel(context.Background(), &channelv1.UpdateModelRequest{ModelPk: 519, PricingInput: 0.3})
	require.ErrorIs(t, err, storageErr)
	require.ErrorContains(t, err, "model saved, but publishing its billing price failed")
	require.Nil(t, resp)
}
