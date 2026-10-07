package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
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
