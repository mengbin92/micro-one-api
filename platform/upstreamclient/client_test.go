package upstreamclient

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/provider"
)

type deniedClient struct {
	channelv1.ChannelServiceClient
	err error
}

func (c deniedClient) ClaimUpstreamExecution(context.Context, *channelv1.ClaimUpstreamExecutionRequest, ...grpc.CallOption) (*channelv1.ClaimUpstreamExecutionReply, error) {
	return nil, c.err
}

func TestCapacityDenialRetainsItsCauseAndMeaning(t *testing.T) {
	for _, code := range []codes.Code{codes.ResourceExhausted, codes.Unavailable} {
		cause := status.Error(code, "denied")
		_, err := ClaimExecution(context.Background(), deniedClient{err: cause}, provider.ExecutionClaim{})
		var capacity *provider.ExecutionCapacityError
		if errors.As(err, &capacity) != (code == codes.ResourceExhausted) || !errors.Is(err, cause) {
			t.Fatalf("code=%v error=%v: capacity must be distinct from an unavailable owner", code, err)
		}
	}
}
