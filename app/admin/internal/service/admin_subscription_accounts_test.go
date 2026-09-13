package service

import (
	"context"
	"testing"

	adminv1 "micro-one-api/api/admin/v1"
	channelv1 "micro-one-api/api/channel/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingSubscriptionAccountsClient struct {
	channelv1.ChannelServiceClient
	err error
}

func (c *failingSubscriptionAccountsClient) ListSubscriptionAccounts(ctx context.Context, req *channelv1.ListSubscriptionAccountsRequest, opts ...grpc.CallOption) (*channelv1.ListSubscriptionAccountsResponse, error) {
	return nil, c.err
}

// A deadline-exhausted fan-out (the admin summary spends its budget on
// earlier RPCs) must still surface as Internal to the caller, never be
// mistaken for "no accounts". The log-level branch (Info vs Error) is a
// pure observability concern; the contract under test is the propagation.
func TestListSubscriptionAccounts_PropagatesDeadlineExceeded(t *testing.T) {
	svc := NewAdminService(nil, nil, &failingSubscriptionAccountsClient{
		err: status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
	}, nil)
	_, err := svc.ListSubscriptionAccounts(context.Background(), &adminv1.AdminListSubscriptionAccountsRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
}

func TestListSubscriptionAccounts_PropagatesBackendFailure(t *testing.T) {
	svc := NewAdminService(nil, nil, &failingSubscriptionAccountsClient{
		err: status.Error(codes.Unavailable, "connection refused"),
	}, nil)
	_, err := svc.ListSubscriptionAccounts(context.Background(), &adminv1.AdminListSubscriptionAccountsRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
}
