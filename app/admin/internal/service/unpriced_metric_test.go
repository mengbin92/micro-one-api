package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	channelv1 "micro-one-api/api/channel/v1"
	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/platform/metrics"
)

type unpricedOptions struct {
	adminbiz.SystemOptionsRepo
	raw string
	err error
}

func (s *unpricedOptions) Get(context.Context, string) (string, error) { return s.raw, s.err }

type unpricedMetricClient struct {
	channelv1.ChannelServiceClient
	calls      int
	countCalls int
	priced     []string
	operator   []string
	response   *channelv1.CountUnpricedRoutedModelsResponse
	err        error
	panicValue any
}

func (c *unpricedMetricClient) ListUnpricedRoutedModels(context.Context, *channelv1.ListUnpricedRoutedModelsRequest, ...grpc.CallOption) (*channelv1.ListUnpricedRoutedModelsResponse, error) {
	c.calls++
	return &channelv1.ListUnpricedRoutedModelsResponse{}, nil
}

func (c *unpricedMetricClient) CountUnpricedRoutedModels(ctx context.Context, req *channelv1.ListUnpricedRoutedModelsRequest, _ ...grpc.CallOption) (*channelv1.CountUnpricedRoutedModelsResponse, error) {
	c.countCalls++
	if c.panicValue != nil {
		panic(c.panicValue)
	}
	c.priced = append([]string(nil), req.PricedModelIds...)
	md, _ := metadata.FromOutgoingContext(ctx)
	c.operator = md.Get("x-operator-authorization")
	return c.response, c.err
}

func TestUnpricedEvaluationPreservesCountsWhenPricesUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		err       error
	}{
		{name: "read_failure", err: errors.New("read unavailable")},
		{name: "invalid_json", raw: "{"},
		{name: "missing"},
		{name: "null", raw: "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &unpricedMetricClient{}
			svc := NewAdminService(nil, nil, client, adminbiz.NewSystemOptionsUsecase(&unpricedOptions{raw: tc.raw, err: tc.err}))
			channel := metrics.UnpricedRoutedModels.WithLabelValues("channel")
			subscription := metrics.UnpricedRoutedModels.WithLabelValues("subscription")
			channel.Set(7)
			subscription.Set(4)
			metrics.UnpricedRoutedEvaluationSuccess.Set(1)
			metrics.UnpricedRoutedLastSuccessTimestamp.Set(123)
			err := svc.EvaluateUnpricedRoutedMetric(context.Background())
			require.Error(t, err)
			require.Zero(t, client.calls+client.countCalls, "do not turn an unreadable price set into unpriced models")
			require.Zero(t, testutil.ToFloat64(metrics.UnpricedRoutedEvaluationSuccess))
			require.EqualValues(t, 123, testutil.ToFloat64(metrics.UnpricedRoutedLastSuccessTimestamp))
			require.EqualValues(t, 7, testutil.ToFloat64(channel))
			require.EqualValues(t, 4, testutil.ToFloat64(subscription))
		})
	}
}

func TestUnpricedEvaluationUsesBackgroundCountsAndRecovers(t *testing.T) {
	for _, raw := range []string{`{" paid ":{"input_price":1},"free":{"input_price":0,"output_price":0}}`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			client := &unpricedMetricClient{response: &channelv1.CountUnpricedRoutedModelsResponse{ChannelCount: 2, SubscriptionCount: 3}}
			svc := NewAdminService(nil, nil, client, adminbiz.NewSystemOptionsUsecase(&unpricedOptions{raw: raw}))
			metrics.UnpricedRoutedLastSuccessTimestamp.Set(0)
			ctx := metadata.AppendToOutgoingContext(WithOperatorCredential(context.Background(), "browser-session"), "x-operator-authorization", "Bearer stale-session")
			require.NoError(t, svc.EvaluateUnpricedRoutedMetric(ctx))
			if raw == `{}` {
				require.Empty(t, client.priced)
			} else {
				require.ElementsMatch(t, []string{"paid", "free"}, client.priced)
			}
			require.Empty(t, client.operator, "the worker must not forward operator credentials")
			require.Zero(t, client.calls, "the detailed operator RPC is never used by the worker")
			require.EqualValues(t, 2, testutil.ToFloat64(metrics.UnpricedRoutedModels.WithLabelValues("channel")))
			require.EqualValues(t, 3, testutil.ToFloat64(metrics.UnpricedRoutedModels.WithLabelValues("subscription")))
			require.EqualValues(t, 1, testutil.ToFloat64(metrics.UnpricedRoutedEvaluationSuccess))
			last := testutil.ToFloat64(metrics.UnpricedRoutedLastSuccessTimestamp)
			require.Positive(t, last)
			client.err = errors.New("channel unavailable")
			require.ErrorIs(t, svc.EvaluateUnpricedRoutedMetric(ctx), client.err)
			require.Zero(t, testutil.ToFloat64(metrics.UnpricedRoutedEvaluationSuccess))
			require.Equal(t, last, testutil.ToFloat64(metrics.UnpricedRoutedLastSuccessTimestamp))
			require.EqualValues(t, 2, testutil.ToFloat64(metrics.UnpricedRoutedModels.WithLabelValues("channel")))
			require.EqualValues(t, 3, testutil.ToFloat64(metrics.UnpricedRoutedModels.WithLabelValues("subscription")))
			client.err = nil
			client.response = &channelv1.CountUnpricedRoutedModelsResponse{}
			require.NoError(t, svc.EvaluateUnpricedRoutedMetric(ctx))
			require.EqualValues(t, 1, testutil.ToFloat64(metrics.UnpricedRoutedEvaluationSuccess))
			require.GreaterOrEqual(t, testutil.ToFloat64(metrics.UnpricedRoutedLastSuccessTimestamp), last)
			require.Zero(t, testutil.ToFloat64(metrics.UnpricedRoutedModels.WithLabelValues("channel")))
			require.Zero(t, testutil.ToFloat64(metrics.UnpricedRoutedModels.WithLabelValues("subscription")))
		})
	}
}

func TestUnpricedWorkerRecoversAfterEvaluationPanic(t *testing.T) {
	client := &unpricedMetricClient{panicValue: "upstream adapter panicked", response: &channelv1.CountUnpricedRoutedModelsResponse{ChannelCount: 1, SubscriptionCount: 2}}
	svc := NewAdminService(nil, nil, client, adminbiz.NewSystemOptionsUsecase(&unpricedOptions{raw: `{}`}))
	worker := NewUnpricedRoutedMetricWorker(svc, 60*time.Second)
	channel := metrics.UnpricedRoutedModels.WithLabelValues("channel")
	subscription := metrics.UnpricedRoutedModels.WithLabelValues("subscription")
	channel.Set(7)
	subscription.Set(4)
	metrics.UnpricedRoutedEvaluationSuccess.Set(1)
	metrics.UnpricedRoutedLastSuccessTimestamp.Set(123)
	require.ErrorContains(t, worker.evaluateOnce(context.Background()), "unpriced model audit panic")
	require.Zero(t, testutil.ToFloat64(metrics.UnpricedRoutedEvaluationSuccess))
	require.EqualValues(t, 123, testutil.ToFloat64(metrics.UnpricedRoutedLastSuccessTimestamp))
	require.EqualValues(t, 7, testutil.ToFloat64(channel))
	require.EqualValues(t, 4, testutil.ToFloat64(subscription))
	client.panicValue = nil
	require.NoError(t, worker.evaluateOnce(context.Background()))
	require.EqualValues(t, 1, testutil.ToFloat64(metrics.UnpricedRoutedEvaluationSuccess))
	require.Greater(t, testutil.ToFloat64(metrics.UnpricedRoutedLastSuccessTimestamp), float64(123))
	require.EqualValues(t, 1, testutil.ToFloat64(channel))
	require.EqualValues(t, 2, testutil.ToFloat64(subscription))
}
