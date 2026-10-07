package service

import (
	"context"
	"github.com/stretchr/testify/require"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
	"time"
)

type purchaseActor struct{ calls int }

func (r *purchaseActor) Query(context.Context, string, string, string) (authorization.ResourceAuthorization, error) {
	panic("self purchase does not need management grants")
}
func (r *purchaseActor) ResolveActor(_ context.Context, point, credential string) (authorization.Actor, string, error) {
	r.calls++
	if point != "billing.self" || credential != "user-session" {
		return authorization.Actor{}, "iam", authorization.ErrDenied
	}
	return authorization.Actor{UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}, "iam", nil
}

type purchaseLedger struct {
	biz.LedgerRepo
	calls int
}

func (r *purchaseLedger) CreateLedger(context.Context, *biz.Ledger) error { r.calls++; return nil }
func TestIAMSubscriptionPurchaseChecksSelfBeforeCharge(t *testing.T) {
	for _, user := range []string{"1", "2"} {
		t.Run(user, func(t *testing.T) {
			account := &minimalAccountRepo{balance: 1000}
			ledger := &purchaseLedger{}
			resolver := &purchaseActor{}
			uc := biz.NewBillingUsecase(account, nil, ledger, nil, nil)
			uc.SetAuthorization(resolver)
			svc := NewBillingService(uc, nil, nil, nil)
			ctx := serviceidentity.WithPrincipal(authorization.WithCredential(authorization.WithExternal(context.Background()), "user-session"), serviceidentity.Principal{Name: "admin", Dedicated: true})
			_, err := svc.PurchaseSubscription(ctx, &billingv1.PurchaseSubscriptionRequest{UserId: user, PriceAmount: 100, GroupId: 5, RequestId: "renewal-1"})
			if user == "1" {
				require.NoError(t, err)
				require.EqualValues(t, 900, account.balance)
				require.Equal(t, 1, ledger.calls)
			} else {
				require.Error(t, err)
				require.EqualValues(t, 1000, account.balance)
				require.Zero(t, ledger.calls)
			}
			require.Positive(t, resolver.calls)
		})
	}
}
