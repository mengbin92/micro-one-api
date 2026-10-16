package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
)

type coverageIAM struct{ identityv1.IAMServiceClient }

func (coverageIAM) GetResourceAuthorization(context.Context, *identityv1.ResourceAuthorizationRequest, ...grpc.CallOption) (*identityv1.ResourceAuthorizationReply, error) {
	return &identityv1.ResourceAuthorizationReply{AuthorizationMode: "iam"}, nil
}
func TestPurchaseSubscriptionReachesIAMOwner(t *testing.T) {
	for _, method := range []string{billingv1.BillingService_PurchaseSubscription_FullMethodName, billingv1.BillingService_GetRoutingCapabilities_FullMethodName, billingv1.BillingService_CreatePaymentOrder_FullMethodName} {
		t.Run(method, func(t *testing.T) {
			ctx := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(authorization.WithCredential(authorization.WithExternal(context.Background()), "user-session"), serviceidentity.Principal{Name: "admin", Dedicated: true}), method)
			called := false
			_, err := authz.CoverageUnaryInterceptor(authz.NewClient("billing", coverageIAM{}), "billing.accounts.read", billingReadyMethods)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, any) (any, error) { called = true; return nil, nil })
			require.NoError(t, err)
			require.True(t, called, "self-service must reach the owner handler")
		})
	}
}

func TestVendorReportsCoverageReachesIAMOwner(t *testing.T) {
	for _, method := range []string{billingv1.BillingService_ListVendorReports_FullMethodName, billingv1.BillingService_ImportVendorReport_FullMethodName} {
		t.Run(method, func(t *testing.T) {
			for _, caller := range []struct {
				name      string
				principal serviceidentity.Principal
				allowed   bool
			}{
				{"dedicated_admin", serviceidentity.Principal{Name: "admin", Dedicated: true}, true},
				{"shared_admin", serviceidentity.Principal{Name: "admin"}, false},
				{"other_dedicated_service", serviceidentity.Principal{Name: "relay", Dedicated: true}, false},
			} {
				t.Run(caller.name, func(t *testing.T) {
					ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer user-session"))
					ctx = serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(ctx, caller.principal), method)
					called := false
					info := &grpc.UnaryServerInfo{FullMethod: method}
					_, err := authz.OperatorUnaryInterceptor()(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
						return authz.CoverageUnaryInterceptor(authz.NewClient("billing", coverageIAM{}), "billing.accounts.read", billingReadyMethods)(ctx, req, info, func(ctx context.Context, _ any) (any, error) {
							called = true
							require.Equal(t, "Bearer user-session", authorization.Credential(ctx))
							// Dispatch readiness grants no user operation. The owner's
							// own Prepare/Require decision still controls the response.
							return nil, status.Error(codes.PermissionDenied, "owner operation not granted")
						})
					})
					require.Equal(t, caller.allowed, called, "completed owner must be reached only by its dedicated admin caller")
					require.Equal(t, codes.PermissionDenied, status.Code(err))
					if caller.allowed {
						require.Equal(t, "owner operation not granted", status.Convert(err).Message())
					} else {
						require.Equal(t, "owner execution point incomplete or caller denied", status.Convert(err).Message())
					}
				})
			}
		})
	}
}

func TestVendorReportsCoverageRejectsUnreviewedRPC(t *testing.T) {
	method := "/api.billing.v1.BillingService/UnreviewedVendorReportAction"
	ctx := serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "admin", Dedicated: true})
	called := false
	_, err := authz.CoverageUnaryInterceptor(authz.NewClient("billing", coverageIAM{}), "billing.accounts.read", billingReadyMethods)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, called)
}
