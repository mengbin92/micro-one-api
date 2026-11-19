package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/notify/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

type notificationSenderFunc func(context.Context, *biz.Notification) error

func (f notificationSenderFunc) Send(ctx context.Context, n *biz.Notification) error {
	return f(ctx, n)
}

func TestDispatcherRecoversExpiredLeaseBeforeEachPoll(t *testing.T) {
	ctx := t.Context()
	repo := &Repository{db: dbtest.RoutingContextDB(t, "sqlite")}
	n := &biz.Notification{Type: biz.NotifyTypeEvent, Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
	require.NoError(t, repo.Create(ctx, n))
	require.NoError(t, repo.db.Model(&notificationModel{}).Where("id = ?", n.ID).Updates(map[string]any{
		"status": biz.NotifyStatusProcessing, "processing_at": time.Now().Add(-processingLease - time.Minute).Unix(),
	}).Error)
	sent := 0
	dispatcher := biz.NewDispatcher(biz.NewNotifyUsecase(repo), notificationSenderFunc(func(_ context.Context, item *biz.Notification) error {
		require.Equal(t, n.ID, item.ID)
		sent++
		return nil
	}), time.Second, 20, 3)
	require.NoError(t, dispatcher.DispatchOnce(ctx))
	require.Equal(t, 1, sent)
	stored, err := repo.Get(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, biz.NotifyStatusSent, stored.Status)
}

func TestDispatcherClaimsOnlyTheNotificationItIsSending(t *testing.T) {
	ctx := t.Context()
	repo := &Repository{db: dbtest.RoutingContextDB(t, "sqlite")}
	first := &biz.Notification{Type: biz.NotifyTypeEvent, Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
	second := *first
	require.NoError(t, repo.Create(ctx, first))
	require.NoError(t, repo.Create(ctx, &second))
	dispatcher := biz.NewDispatcher(biz.NewNotifyUsecase(repo), notificationSenderFunc(func(ctx context.Context, item *biz.Notification) error {
		stored, err := repo.Get(ctx, item.ID)
		require.NoError(t, err)
		require.Equal(t, biz.NotifyStatusProcessing, stored.Status)
		require.Equal(t, stored.ProcessingAt, item.ProcessingAt)
		if item.ID == first.ID {
			waiting, err := repo.Get(ctx, second.ID)
			require.NoError(t, err)
			require.Equal(t, biz.NotifyStatusPending, waiting.Status)
		}
		return nil
	}), time.Second, 20, 3)
	require.NoError(t, dispatcher.DispatchOnce(ctx))
	for _, id := range []int64{first.ID, second.ID} {
		stored, err := repo.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, biz.NotifyStatusSent, stored.Status)
	}
}

func TestDispatcherPersistsAcknowledgedDeliveryOnShutdown(t *testing.T) {
	repo := &Repository{db: dbtest.RoutingContextDB(t, "sqlite")}
	n := &biz.Notification{Type: biz.NotifyTypeEvent, Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
	require.NoError(t, repo.Create(t.Context(), n))
	sending := make(chan struct{})
	dispatcher := biz.NewDispatcher(biz.NewNotifyUsecase(repo), notificationSenderFunc(func(ctx context.Context, _ *biz.Notification) error {
		close(sending)
		<-ctx.Done()
		return nil
	}), time.Second, 20, 3)
	stop := dispatcher.Start(t.Context())
	t.Cleanup(stop)
	select {
	case <-sending:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not start delivery")
	}
	stop()
	stored, err := repo.Get(t.Context(), n.ID)
	require.NoError(t, err)
	require.Equal(t, biz.NotifyStatusSent, stored.Status)
}

func TestMemoryRepository_CreateAndGet(t *testing.T) {
	repo := newMemoryRepository()

	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com/hook",
		Subject:   "alert",
		Content:   "service down",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}

	err := repo.Create(context.Background(), n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}

	got, err := repo.Get(context.Background(), n.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subject != "alert" {
		t.Fatalf("expected alert, got %s", got.Subject)
	}
}

func TestMemoryRepository_GetNotFound(t *testing.T) {
	repo := newMemoryRepository()
	_, err := repo.Get(context.Background(), 999)
	if err != biz.ErrNotificationNotFound {
		t.Fatalf("expected ErrNotificationNotFound, got %v", err)
	}
}

func TestMemoryRepository_List(t *testing.T) {
	repo := newMemoryRepository()
	_ = repo.Create(context.Background(), &biz.Notification{Type: biz.NotifyTypeWebhook, Recipient: "a", Status: biz.NotifyStatusPending, CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.Notification{Type: biz.NotifyTypeEmail, Recipient: "b", Status: biz.NotifyStatusSent, CreatedAt: time.Now()})

	t.Run("all", func(t *testing.T) {
		items, total, err := repo.List(context.Background(), 1, 20, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 1 from default + 2 created
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3, got %d", len(items))
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		items, total, err := repo.List(context.Background(), 1, 20, biz.NotifyTypeEmail, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(items) != 1 || items[0].Type != biz.NotifyTypeEmail {
			t.Fatalf("unexpected items: %+v", items)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		items, total, err := repo.List(context.Background(), 1, 20, "", biz.NotifyStatusPending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(items) != 1 || items[0].Status != biz.NotifyStatusPending {
			t.Fatalf("unexpected items: %+v", items)
		}
	})
}

func TestMemoryRepository_UpdateStatus(t *testing.T) {
	repo := newMemoryRepository()

	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	_ = repo.Create(context.Background(), n)

	t.Run("mark sent", func(t *testing.T) {
		err := repo.UpdateStatus(context.Background(), n.ID, biz.NotifyStatusSent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := repo.Get(context.Background(), n.ID)
		if got.Status != biz.NotifyStatusSent {
			t.Fatalf("expected sent, got %s", got.Status)
		}
		if got.SentAt.IsZero() {
			t.Fatal("expected SentAt to be set")
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := repo.UpdateStatus(context.Background(), 999, biz.NotifyStatusSent)
		if err != biz.ErrNotificationNotFound {
			t.Fatalf("expected ErrNotificationNotFound, got %v", err)
		}
	})
}

func TestMemoryRepository_ListPendingAndRecordFailure(t *testing.T) {
	repo := newMemoryRepository()
	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	_ = repo.Create(context.Background(), n)

	items, err := repo.ListPending(context.Background(), 20, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].ID != n.ID {
		t.Fatalf("expected created pending notification, got %+v", items)
	}

	if err := repo.RecordFailure(context.Background(), n.ID, 2, "webhook returned status 500"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusPending || got.RetryCount != 1 || got.LastError != "webhook returned status 500" {
		t.Fatalf("expected pending retry 1 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}

	if err := repo.RecordFailure(context.Background(), n.ID, 2, "timeout"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusFailed || got.RetryCount != 2 || got.LastError != "timeout" {
		t.Fatalf("expected failed retry 2 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}

	if err := repo.UpdateStatus(context.Background(), n.ID, biz.NotifyStatusSent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = repo.Get(context.Background(), n.ID)
	if got.LastError != "" {
		t.Fatalf("expected last error to be cleared after sent, got %q", got.LastError)
	}
}

func TestMemoryRepository_ClaimAndCompleteProcessingLease(t *testing.T) {
	repo := newMemoryRepository()
	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	if err := repo.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}

	claimed, err := repo.Get(t.Context(), n.ID)
	require.NoError(t, err)
	other := *claimed
	ok, err := repo.ClaimPending(t.Context(), claimed, 3)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.ClaimPending(t.Context(), &other, 3)
	require.NoError(t, err)
	require.False(t, ok)
	if got, _ := repo.Get(context.Background(), n.ID); got.Status != biz.NotifyStatusProcessing {
		t.Fatalf("second claim changed status to %q", got.Status)
	}

	stale := claimed.ProcessingAt.Add(-time.Second)
	if err := repo.CompleteProcessing(context.Background(), n.ID, stale, biz.NotifyStatusSent, 3, ""); err != biz.ErrNotificationLeaseLost {
		t.Fatalf("stale completion error = %v, want ErrNotificationLeaseLost", err)
	}
	if err := repo.CompleteProcessing(context.Background(), n.ID, claimed.ProcessingAt, biz.NotifyStatusSent, 3, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusSent || !got.ProcessingAt.IsZero() {
		t.Fatalf("completion = status %q processing_at %v", got.Status, got.ProcessingAt)
	}
}
