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
			system := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(authorization.WithCredential(request, ""), serviceidentity.Principal{Name: "monitor", Dedicated: true}), "/api.notify.v1.NotifyService/CreateNotification")
			_, err = uc.CreateNotification(system, biz.NotifyTypeEvent, "", "real worker", "")
			require.NoError(t, err)
			require.Error(t, uc.MarkSent(system, one.ID)) // capability does not cross methods
		})
	}
}

func TestIAMB4NotificationManagementDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres", "memory"} {
		t.Run(driver, func(t *testing.T) {
			r := newMemoryRepository()
			if driver != "memory" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			policy := &authztest.Resolver{ActorID: 7, Scopes: map[string]authorization.QueryScope{"notify.notification.rules.update": authztest.All(), "notify.notification.test": authztest.Resources(41), "notify.notification.acknowledge": authztest.All()}}
			for op, q := range policy.Scopes {
				q.ActorID = 7
				policy.Scopes[op] = q
			}
			uc := biz.NewNotifyUsecase(r)
			uc.SetAuthorization(policy)
			ctx := authztest.Context()
			rule := &biz.NotificationRule{ID: 41, Name: "critical alerts", Event: "alertmanager", Type: biz.NotifyTypeEvent, Enabled: true}
			if driver == "memory" {
				require.ErrorIs(t, uc.SaveNotificationRule(ctx, rule, "must persist audit"), authorization.ErrWriteStorageUnavailable)
				require.NoError(t, r.SaveRule(context.Background(), rule))
				_, err := uc.TestNotificationRule(ctx, 41, "must persist enqueue audit")
				require.ErrorIs(t, err, authorization.ErrWriteStorageUnavailable)
				notification := &biz.Notification{Type: biz.NotifyTypeEvent, Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
				require.NoError(t, r.Create(context.Background(), notification))
				_, err = uc.AcknowledgeNotification(ctx, notification.ID, notification.Revision, "must persist acknowledgment audit")
				require.ErrorIs(t, err, authorization.ErrWriteStorageUnavailable)
				return
			}
			require.NoError(t, uc.SaveNotificationRule(ctx, rule, "create routing rule"))
			require.EqualValues(t, 1, rule.Revision)
			rows, err := uc.ListNotificationRules(ctx)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			_, err = uc.TestNotificationRule(ctx, 42, "hidden test")
			require.Error(t, err)
			n, err := uc.TestNotificationRule(ctx, 41, "delivery test")
			require.NoError(t, err)
			require.Equal(t, biz.NotifyStatusPending, n.Status)
			require.Error(t, uc.MarkSent(ctx, n.ID), "test must not imply status capability")
			ack, err := uc.AcknowledgeNotification(ctx, n.ID, n.Revision, "reviewed delivery test")
			require.NoError(t, err)
			require.EqualValues(t, 7, ack.AcknowledgedBy)
			require.Equal(t, biz.NotifyStatusPending, ack.Status)
			_, err = uc.AcknowledgeNotification(ctx, n.ID, n.Revision, "stale")
			require.ErrorIs(t, err, biz.ErrNotificationConflict)
			policy.Scopes["notify.notification.rules.update"] = authztest.Resources(42)
			rows, err = uc.ListNotificationRules(ctx)
			require.NoError(t, err)
			require.Empty(t, rows)
			rule.Name = "hidden change"
			require.Error(t, uc.SaveNotificationRule(ctx, rule, "denied"))
			if r.db != nil {
				var count int64
				require.NoError(t, r.db.Table("resource_write_audits").Where("actor_user_id = ? AND result = ?", 7, "success").Count(&count).Error)
				require.EqualValues(t, 3, count)
				all := authztest.All()
				all.ActorID = 7
				policy.Scopes["notify.notification.rules.update"] = all
				require.NoError(t, r.db.Exec("DROP TABLE resource_write_audits").Error)
				rule.Name = "audit failure"
				require.Error(t, uc.SaveNotificationRule(ctx, rule, "must rollback"))
				var stored notificationRuleModel
				require.NoError(t, r.db.First(&stored, 41).Error)
				require.Equal(t, "critical alerts", stored.Name)
				require.EqualValues(t, 1, stored.Revision)
				var notificationsBefore, notificationsAfter int64
				require.NoError(t, r.db.Model(&notificationModel{}).Count(&notificationsBefore).Error)
				_, err = uc.TestNotificationRule(ctx, 41, "must rollback enqueue")
				require.Error(t, err)
				require.NoError(t, r.db.Model(&notificationModel{}).Count(&notificationsAfter).Error)
				require.Equal(t, notificationsBefore, notificationsAfter)
			}
		})
	}
}

func TestIAMB4SavedRulesDriveEventDeliveryDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, driver)}
			uc := biz.NewNotifyUsecase(r)
			uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"notify.notification.rules.update": authztest.All()}})
			rule := &biz.NotificationRule{ID: 41, Name: "saved alert route", Event: "alertmanager", Type: biz.NotifyTypeEmail, Recipient: "ops@example.com", Enabled: true}
			require.NoError(t, uc.SaveNotificationRule(authztest.Context(), rule, "install saved route"))
			system := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(authorization.WithExternal(context.Background()), serviceidentity.Principal{Name: "monitor", Dedicated: true}), "/api.notify.v1.NotifyService/CreateNotification")
			rows, err := uc.DispatchEvent(system, "alertmanager", "critical alert", "private event payload", biz.NotifyTypeWebhook, "")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, biz.NotifyTypeEmail, rows[0].Type)
			require.Equal(t, "ops@example.com", rows[0].Recipient)
			stored, err := r.Get(context.Background(), rows[0].ID)
			require.NoError(t, err)
			require.Equal(t, biz.NotifyStatusPending, stored.Status)
			rule.Enabled = false
			require.NoError(t, uc.SaveNotificationRule(authztest.Context(), rule, "disable saved route"))
			rows, err = uc.DispatchEvent(system, "alertmanager", "suppressed alert", "payload", biz.NotifyTypeWebhook, "")
			require.NoError(t, err)
			require.Empty(t, rows, "disabled matching rule suppresses fallback delivery")
			rows, err = uc.DispatchEvent(system, "unconfigured-event", "fallback alert", "payload", biz.NotifyTypeWebhook, "")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, biz.NotifyTypeWebhook, rows[0].Type)
		})
	}
}
