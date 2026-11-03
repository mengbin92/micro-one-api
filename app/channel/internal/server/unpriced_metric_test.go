package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/service"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

type unpricedAuditRepo struct{ biz.ModelRepo }

func (unpricedAuditRepo) ListModels(context.Context, int32, int32, biz.ListModelsFilter) ([]*biz.Model, int64, error) {
	models := []*biz.Model{
		{ModelID: "priced", IsPublic: true, Status: biz.ModelStatusEnabled, ChannelCount: 1},
		{ModelID: "channel", IsPublic: true, Status: biz.ModelStatusEnabled, ChannelCount: 2},
		{ModelID: "subscription", IsPublic: true, Status: biz.ModelStatusEnabled, SubscriptionCount: 3},
		{ModelID: "both", IsPublic: true, Status: biz.ModelStatusEnabled, ChannelCount: 1, SubscriptionCount: 1},
		{ModelID: "disabled", IsPublic: true, Status: biz.ModelStatusDisabled, ChannelCount: 1},
	}
	return models, int64(len(models)), nil
}

func TestUnpricedAuditRPCSeparatesBackgroundAndOperatorCapabilities(t *testing.T) {
	resolver := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{}}
	svc := service.NewChannelService(biz.NewChannelUsecase(nil, nil))
	svc.SetModelUsecase(biz.NewModelUsecase(unpricedAuditRepo{}))
	svc.SetResourceAuthorizer(resolver)
	verifier, err := serviceidentity.NewVerifier(map[string]string{"admin": "dedicated-admin", "relay": "dedicated-relay"}, "shared")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		xgrpc.ServiceIdentityUnaryInterceptor(verifier),
		authz.OperatorUnaryInterceptor(),
		authz.CoverageUnaryInterceptor(authz.NewClient("channel", contractCoverageIAM{}), "channel.channels.list", channelReadyMethods),
	))
	channelv1.RegisterChannelServiceServer(server, svc)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///channel-audit", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := channelv1.NewChannelServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := &channelv1.ListUnpricedRoutedModelsRequest{PricedModelIds: []string{" PRICED "}}
	for _, tc := range []struct {
		name, token, operator string
		code                  codes.Code
	}{
		{"background_admin", "dedicated-admin", "", codes.OK},
		{"shared_token", "shared", "", codes.PermissionDenied},
		{"other_service", "dedicated-relay", "", codes.PermissionDenied},
		{"operator_cannot_use_system_RPC", "dedicated-admin", "verified-session", codes.PermissionDenied},
		{"missing_credential", "", "", codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tc.token)
			if tc.operator != "" {
				call = metadata.AppendToOutgoingContext(call, "x-operator-authorization", tc.operator)
			}
			resp, err := client.CountUnpricedRoutedModels(call, req)
			require.Equal(t, tc.code, status.Code(err))
			if tc.code == codes.OK {
				require.EqualValues(t, 2, resp.ChannelCount)
				require.EqualValues(t, 2, resp.SubscriptionCount)
			}
		})
	}
	t.Run("background_cannot_read_operator_details", func(t *testing.T) {
		call := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer dedicated-admin")
		_, err := client.ListUnpricedRoutedModels(call, req)
		require.Equal(t, codes.PermissionDenied, status.Code(err))
		require.Equal(t, "authorization denied", status.Convert(err).Message())
	})
	for _, missing := range []string{"channel.model.list", "billing.pricing.read", "channel.model_mapping.read", ""} {
		t.Run("operator_missing_"+missing, func(t *testing.T) {
			resolver.Scopes = map[string]authorization.QueryScope{}
			for _, operation := range []string{"channel.model.list", "billing.pricing.read", "channel.model_mapping.read"} {
				if operation != missing {
					resolver.Scopes[operation] = authztest.All()
				}
			}
			call := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer dedicated-admin", "x-operator-authorization", "verified-session")
			resp, err := client.ListUnpricedRoutedModels(call, req)
			if missing == "" {
				require.NoError(t, err)
				require.Len(t, resp.Models, 3)
			} else {
				require.Equal(t, codes.PermissionDenied, status.Code(err))
				require.Equal(t, "authorization denied", status.Convert(err).Message(), "must reach the owner checks")
			}
		})
	}
}
