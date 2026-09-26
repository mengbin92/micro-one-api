package integration

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notifyv1 "micro-one-api/api/notify/v1"
	notifybiz "micro-one-api/app/notify/internal/biz"
	notifyservice "micro-one-api/app/notify/internal/service"
)

// ========== Notify Service Tests ==========

type testNotifyRepo struct {
	notifications []*notifybiz.Notification
	idSeq         int64
}

func (r *testNotifyRepo) Create(ctx context.Context, n *notifybiz.Notification) error {
	r.idSeq++
	n.ID = r.idSeq
	n.Status = "pending"
	r.notifications = append(r.notifications, n)
	return nil
}

func (r *testNotifyRepo) Get(ctx context.Context, id int64) (*notifybiz.Notification, error) {
	for _, n := range r.notifications {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, notifybiz.ErrNotificationNotFound
}

func (r *testNotifyRepo) List(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*notifybiz.Notification, int64, error) {
	var result []*notifybiz.Notification
	for _, n := range r.notifications {
		if (notifyType == "" || n.Type == notifyType) && (status == "" || n.Status == status) {
			result = append(result, n)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testNotifyRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	for _, n := range r.notifications {
		if n.ID == id {
			n.Status = status
			return nil
		}
	}
	return notifybiz.ErrNotificationNotFound
}

func (r *testNotifyRepo) ListPending(ctx context.Context, limit int32, maxRetry int) ([]*notifybiz.Notification, error) {
	var result []*notifybiz.Notification
	for _, n := range r.notifications {
		if n.Status == notifybiz.NotifyStatusPending && n.RetryCount < maxRetry {
			result = append(result, n)
			if int32(len(result)) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (r *testNotifyRepo) MarkFailed(ctx context.Context, id int64) error {
	for _, n := range r.notifications {
		if n.ID == id {
			n.Status = notifybiz.NotifyStatusFailed
			return nil
		}
	}
	return notifybiz.ErrNotificationNotFound
}

func (r *testNotifyRepo) RecordFailure(ctx context.Context, id int64, maxRetry int, lastError string) error {
	for _, n := range r.notifications {
		if n.ID == id {
			n.RetryCount++
			if n.RetryCount >= maxRetry {
				n.Status = notifybiz.NotifyStatusFailed
			}
			n.LastError = lastError
			return nil
		}
	}
	return notifybiz.ErrNotificationNotFound
}

func setupNotifyService(t *testing.T, addr string) (func(), notifyv1.NotifyServiceClient) {
	repo := &testNotifyRepo{}
	uc := notifybiz.NewNotifyUsecase(repo)
	svc := notifyservice.NewNotifyService(uc, "")

	server := grpc.NewServer()
	notifyv1.RegisterNotifyServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("notify server error: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
		lis.Close()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	return cleanup, notifyv1.NewNotifyServiceClient(conn)
}

func TestNotifyIntegration(t *testing.T) {
	cleanup, client := setupNotifyService(t, "127.0.0.1:19013")
	defer cleanup()

	ctx := context.Background()

	t.Run("CreateAndGet", func(t *testing.T) {
		createResp, err := client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
			Type:      "email",
			Recipient: "test@example.com",
			Subject:   "Test Notification",
			Content:   "This is a test notification",
		})
		if err != nil {
			t.Fatalf("CreateNotification failed: %v", err)
		}
		if createResp.Notification.Id == 0 {
			t.Fatal("expected non-zero notification ID")
		}

		getResp, err := client.GetNotification(ctx, &notifyv1.GetNotificationRequest{
			Id: createResp.Notification.Id,
		})
		if err != nil {
			t.Fatalf("GetNotification failed: %v", err)
		}
		if getResp.Notification.Subject != "Test Notification" {
			t.Fatalf("expected subject 'Test Notification', got '%s'", getResp.Notification.Subject)
		}
	})

	t.Run("ListAndFilter", func(t *testing.T) {
		_, _ = client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
			Type: "sms", Recipient: "+1234567890", Subject: "SMS Test", Content: "SMS content",
		})

		resp, err := client.ListNotifications(ctx, &notifyv1.ListNotificationsRequest{
			Page:     1,
			PageSize: 10,
			Type:     "email",
		})
		if err != nil {
			t.Fatalf("ListNotifications failed: %v", err)
		}
		if resp.Total < 1 {
			t.Fatalf("expected at least 1 email notification, got %d", resp.Total)
		}
	})

	t.Run("UpdateStatus", func(t *testing.T) {
		createResp, _ := client.CreateNotification(ctx, &notifyv1.CreateNotificationRequest{
			Type: "webhook", Recipient: "http://example.com", Subject: "Webhook", Content: "{}",
		})

		_, err := client.UpdateNotificationStatus(ctx, &notifyv1.UpdateNotificationStatusRequest{
			Id:     createResp.Notification.Id,
			Status: "sent",
		})
		if err != nil {
			t.Fatalf("UpdateNotificationStatus failed: %v", err)
		}

		getResp, _ := client.GetNotification(ctx, &notifyv1.GetNotificationRequest{
			Id: createResp.Notification.Id,
		})
		if getResp.Notification.Status != "sent" {
			t.Fatalf("expected status 'sent', got '%s'", getResp.Notification.Status)
		}
	})
}
