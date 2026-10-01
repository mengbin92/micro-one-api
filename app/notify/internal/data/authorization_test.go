package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
	"time"
)

func TestIAMB4NotifyOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres", "memory"} {
		t.Run(driver, func(t *testing.T) {
			r := newMemoryRepository()
			if driver != "memory" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			one := &biz.Notification{Type: biz.NotifyTypeEvent, Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
			two := &biz.Notification{Type: biz.NotifyTypeEvent, Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
			require.NoError(t, r.Create(context.Background(), one))
			require.NoError(t, r.Create(context.Background(), two))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"notify.notification.list": authztest.Resources(one.ID), "notify.notification.read": authztest.Resources(one.ID)}}
			uc := biz.NewNotifyUsecase(r)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			rows, total, err := uc.ListNotifications(request, 1, 1, "", "")
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			_, err = uc.GetNotification(request, two.ID)
			require.Error(t, err)
			_, err = uc.CreateNotification(request, biz.NotifyTypeEvent, "", "forged", "")
			require.Error(t, err)
			require.Error(t, uc.MarkSent(request, one.ID))
			system := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(request, serviceidentity.Principal{Name: "monitor", Dedicated: true}), "/api.notify.v1.NotifyService/CreateNotification")
			_, err = uc.CreateNotification(system, biz.NotifyTypeEvent, "", "real worker", "")
			require.NoError(t, err)
			require.Error(t, uc.MarkSent(system, one.ID)) // capability does not cross methods
		})
	}
}
