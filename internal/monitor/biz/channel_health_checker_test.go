package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"

	"google.golang.org/grpc"
)

type checkerChannelClient struct {
	channelv1.ChannelServiceClient
	channels   []*commonv1.ChannelSummary
	details    map[int64]*commonv1.ChannelInfo
	healthReqs []*channelv1.RecordChannelHealthRequest
}

func (c *checkerChannelClient) ListChannels(ctx context.Context, req *channelv1.ListChannelsRequest, opts ...grpc.CallOption) (*channelv1.ListChannelsResponse, error) {
	return &channelv1.ListChannelsResponse{Channels: c.channels, Total: int64(len(c.channels))}, nil
}

func (c *checkerChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	return &channelv1.GetChannelReply{Channel: c.details[req.ChannelId]}, nil
}

func (c *checkerChannelClient) RecordChannelHealth(ctx context.Context, req *channelv1.RecordChannelHealthRequest, opts ...grpc.CallOption) (*channelv1.RecordChannelHealthResponse, error) {
	c.healthReqs = append(c.healthReqs, req)
	return &channelv1.RecordChannelHealthResponse{Success: true, Message: "ok"}, nil
}

func TestChannelHealthChecker_CheckOnceRecordsSuccess(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()

	client := &checkerChannelClient{
		channels: []*commonv1.ChannelSummary{{Id: 1, Type: 1, Status: 1}},
		details: map[int64]*commonv1.ChannelInfo{
			1: {Id: 1, Type: 1, Status: 1, BaseUrl: upstream.URL + "/v1", Key: "sk-test"},
		},
	}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Enabled: true, Timeout: time.Second})
	checker.CheckOnce(context.Background())

	if len(client.healthReqs) != 1 || !client.healthReqs[0].Success || client.healthReqs[0].ChannelId != 1 {
		t.Fatalf("health requests = %+v", client.healthReqs)
	}
}

func TestChannelHealthChecker_CheckOnceRecordsFailure(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer upstream.Close()

	client := &checkerChannelClient{
		channels: []*commonv1.ChannelSummary{{Id: 1, Type: 1, Status: 1}},
		details: map[int64]*commonv1.ChannelInfo{
			1: {Id: 1, Type: 1, Status: 1, BaseUrl: upstream.URL + "/v1", Key: "sk-test"},
		},
	}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Enabled: true, Timeout: time.Second})
	checker.CheckOnce(context.Background())

	if len(client.healthReqs) != 1 || client.healthReqs[0].Success || client.healthReqs[0].ChannelId != 1 {
		t.Fatalf("health requests = %+v", client.healthReqs)
	}
}

func TestChannelHealthChecker_CheckOnceSkipsUnsupportedProvider(t *testing.T) {
	client := &checkerChannelClient{
		channels: []*commonv1.ChannelSummary{{Id: 1, Type: 2, Status: 1}},
		details:  map[int64]*commonv1.ChannelInfo{},
	}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Enabled: true, Timeout: time.Second})
	checker.CheckOnce(context.Background())

	if len(client.healthReqs) != 0 {
		t.Fatalf("health requests = %+v, want none", client.healthReqs)
	}
}
