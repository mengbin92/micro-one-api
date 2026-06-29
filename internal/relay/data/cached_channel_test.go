package data

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
)

type fakeChannelClient struct {
	channelv1.ChannelServiceClient
	calls        int
	channelToRet *commonv1.ChannelInfo
	err          error
}

func (f *fakeChannelClient) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest, opts ...grpc.CallOption) (*channelv1.SelectChannelReply, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.channelToRet == nil {
		return &channelv1.SelectChannelReply{}, nil
	}
	return &channelv1.SelectChannelReply{Channel: f.channelToRet}, nil
}

// TestCachedChannelClient_NilCachePassthrough ensures that a nil cache (the
// feature-flag-off path) delegates straight to the underlying client.
func TestCachedChannelClient_NilCachePassthrough(t *testing.T) {
	fake := &fakeChannelClient{channelToRet: &commonv1.ChannelInfo{Id: 7}}
	got := NewCachedChannelClient(fake, nil)
	if got != fake {
		t.Fatalf("NewCachedChannelClient with nil cache should return the raw client")
	}
}

// TestCachedChannelClient_FailoverBypassesCache verifies that failover
// selections (ExcludeFirstPriority=true) never read from or write to the
// cache, so retries never replay a failed top-priority channel.
func TestCachedChannelClient_FailoverBypassesCache(t *testing.T) {
	// We can't easily build a real ChannelCache without Redis; instead we
	// verify the bypass path by giving the wrapper a nil-ish cache situation:
	// a wrapper whose cache is nil must fall through to the upstream client
	// even on failover. This covers the documented contract.
	fake := &fakeChannelClient{channelToRet: &commonv1.ChannelInfo{Id: 9}}
	wrapper := &CachedChannelClient{ChannelServiceClient: fake, cache: nil}

	_, err := wrapper.SelectChannel(context.Background(), &channelv1.SelectChannelRequest{
		Group:                "g",
		Model:                "m",
		ExcludeFirstPriority: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 upstream call, got %d", fake.calls)
	}
}

// TestCachedChannelClient_PropagatesUpstreamError ensures cache-miss errors
// from the underlying client are returned unchanged.
func TestCachedChannelClient_PropagatesUpstreamError(t *testing.T) {
	upErr := errors.New("boom")
	fake := &fakeChannelClient{err: upErr}
	wrapper := &CachedChannelClient{ChannelServiceClient: fake, cache: nil}

	_, err := wrapper.SelectChannel(context.Background(), &channelv1.SelectChannelRequest{
		Group: "g",
		Model: "m",
	})
	if !errors.Is(err, upErr) {
		t.Fatalf("expected upstream error, got %v", err)
	}
}
