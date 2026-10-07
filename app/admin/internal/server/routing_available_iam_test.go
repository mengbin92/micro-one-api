package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/channelclient"
	"micro-one-api/app/admin/internal/data/routingaccess"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/security/serviceidentity"
)

type availableIAM struct{ identityv1.IAMServiceClient }

func (availableIAM) GetResourceAuthorization(context.Context, *identityv1.ResourceAuthorizationRequest, ...grpc.CallOption) (*identityv1.ResourceAuthorizationReply, error) {
	// A member has no channel directory or billing policy management grants.
	return &identityv1.ResourceAuthorizationReply{AuthorizationMode: "iam", ActorUserId: 42}, nil
}
func referenceOwnerCall(ctx context.Context, owner, method, point, op string) error {
	md, _ := metadata.FromOutgoingContext(ctx)
	ctx = authorization.WithCredential(authorization.WithExternal(ctx), "")
	if values := md.Get("x-operator-authorization"); len(values) == 1 {
		ctx = authorization.WithCredential(ctx, values[0])
	}
	ctx = serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "admin", Dedicated: true}), method)
	client := authz.NewClient(owner, availableIAM{})
	_, err := authz.CoverageUnaryInterceptor(client, point, []string{method})(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
		if serviceidentity.HasSystemCapability(ctx, method) {
			return nil, nil
		}
		_, err := client.Query(ctx, point, op, authorization.Credential(ctx))
		return nil, err
	})
	return err
}

type availableIdentity struct {
	routingSessionFake
	factsCalls int
}

func (f *availableIdentity) GetUserRoutingFacts(ctx context.Context, req *identityv1.GetUserRoutingFactsRequest, _ ...grpc.CallOption) (*identityv1.GetUserRoutingFactsReply, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	if req.UserId != 42 || len(md.Get("x-operator-authorization")) != 1 || md.Get("x-operator-authorization")[0] != "Bearer user-session" {
		return nil, status.Error(codes.PermissionDenied, "self identity lost")
	}
	f.factsCalls++
	return &identityv1.GetUserRoutingFactsReply{Facts: &commonv1.RoutingSubjectFacts{DefaultGroupId: 1, AccessRevision: 1, PublicGroupAccess: "all"}}, nil
}

type availableChannel struct{ channelv1.ChannelServiceClient }

func (availableChannel) ListRoutingGroups(ctx context.Context, _ *channelv1.ListRoutingGroupsRequest, _ ...grpc.CallOption) (*channelv1.ListRoutingGroupsReply, error) {
	if err := referenceOwnerCall(ctx, "channel", channelv1.ChannelService_ListRoutingGroups_FullMethodName, "channel.routing_groups.list", "channel.routing_group.list"); err != nil {
		return nil, err
	}
	return &channelv1.ListRoutingGroupsReply{Groups: []*channelv1.RoutingGroup{{Id: 1, Key: "default", Status: "enabled", AccessMode: "public"}, {Id: 2, Key: "hidden", Status: "enabled", AccessMode: "restricted"}}}, nil
}
func (availableChannel) GetRoutingGroup(ctx context.Context, req *channelv1.GetRoutingGroupRequest, _ ...grpc.CallOption) (*channelv1.RoutingGroupDetail, error) {
	if err := referenceOwnerCall(ctx, "channel", channelv1.ChannelService_GetRoutingGroup_FullMethodName, "channel.routing_groups.read", "channel.routing_group.read"); err != nil {
		return nil, err
	}
	return &channelv1.RoutingGroupDetail{Group: &channelv1.RoutingGroup{Id: req.Id, Key: "default", Status: "enabled", AccessMode: "public"}}, nil
}
func (availableChannel) ListAvailableModels(ctx context.Context, _ *channelv1.ListAvailableModelsRequest, _ ...grpc.CallOption) (*channelv1.ListAvailableModelsReply, error) {
	if err := referenceOwnerCall(ctx, "channel", channelv1.ChannelService_ListAvailableModels_FullMethodName, "channel.routing_groups.read", "channel.routing_group.read"); err != nil {
		return nil, err
	}
	return &channelv1.ListAvailableModelsReply{Models: []string{"model"}}, nil
}

type availableBilling struct{ billingv1.BillingServiceClient }

func (availableBilling) GetRoutingGroupPrice(ctx context.Context, req *billingv1.GetRoutingGroupPriceRequest, _ ...grpc.CallOption) (*billingv1.GetRoutingGroupPriceReply, error) {
	if err := referenceOwnerCall(ctx, "billing", billingv1.BillingService_GetRoutingGroupPrice_FullMethodName, "billing.routing_policy", "billing.routing_policy.read"); err != nil {
		return nil, err
	}
	if req.UserId != 42 || req.RoutingGroupId != 1 {
		return nil, status.Error(codes.PermissionDenied, "unavailable group quoted")
	}
	return &billingv1.GetRoutingGroupPriceReply{PriceRatio: 1}, nil
}
func TestRoutingAvailableIAMMember(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	identity := &availableIdentity{}
	channel := availableChannel{}
	svc := service.NewAdminService(nil, identity, nil, nil)
	svc.SetRoutingAccessUsecase(biz.NewRoutingAccessUsecase(channelclient.NewRoutingGroupReader(channel), routingaccess.NewRepo(identity, channel, availableBilling{})))
	srv := NewHTTPServer(":0", svc, nil)
	req := httptest.NewRequest("GET", "/api/v1/routing-groups/available?page_size=200&page_token=", nil)
	req.Header.Set("Authorization", "Bearer user-session")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, identity.factsCalls)
	require.Contains(t, w.Body.String(), `"key":"default"`)
	require.NotContains(t, w.Body.String(), "hidden")
}

func TestRoutingManagementKeepsOperatorPermissionChecks(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	ctx := metadata.NewOutgoingContext(service.WithOperatorCredential(context.Background(), "user-session"), metadata.Pairs("x-operator-authorization", "Bearer user-session"))
	// The same adapters must keep requiring management permissions outside the
	// internally marked availability path. Reading availability must not mutate
	// the caller's original metadata or turn later requests into system calls.
	channel := availableChannel{}
	reader := channelclient.NewRoutingGroupReader(channel)
	svc := service.NewAdminService(nil, &availableIdentity{}, nil, nil)
	svc.SetRoutingAccessUsecase(biz.NewRoutingAccessUsecase(reader, routingaccess.NewRepo(&availableIdentity{}, channel, availableBilling{})))
	_, err := svc.AvailableGroups(ctx, 42, routing.GroupListRequest{PageSize: 200})
	require.NoError(t, err)
	_, err = reader.List(ctx, routing.GroupListRequest{PageSize: 200})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = reader.Get(ctx, 1)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = routingaccess.NewRepo(&availableIdentity{}, channel, availableBilling{}).Price(ctx, 1, 42)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
