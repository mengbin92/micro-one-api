package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/config/internal/biz"
)

func TestMemoryRepository_Get(t *testing.T) {
	repo := newMemoryRepository()

	t.Run("existing key", func(t *testing.T) {
		entry, err := repo.Get(context.Background(), "default", "theme")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if entry.Value != "dark" {
			t.Fatalf("expected dark, got %s", entry.Value)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		_, err := repo.Get(context.Background(), "default", "missing")
		if err != biz.ErrConfigNotFound {
			t.Fatalf("expected ErrConfigNotFound, got %v", err)
		}
	})
}

func TestMemoryRepository_Set(t *testing.T) {
	repo := newMemoryRepository()

	entry := &biz.ConfigEntry{
		Namespace: "default",
		Key:       "lang",
		Value:     "zh",
		Comment:   "language",
		UpdatedAt: time.Now(),
	}

	err := repo.Set(context.Background(), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := repo.Get(context.Background(), "default", "lang")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value != "zh" {
		t.Fatalf("expected zh, got %s", got.Value)
	}
}

func TestMemoryRepository_Delete(t *testing.T) {
	repo := newMemoryRepository()

	t.Run("existing", func(t *testing.T) {
		err := repo.Delete(context.Background(), "default", "theme")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = repo.Get(context.Background(), "default", "theme")
		if err != biz.ErrConfigNotFound {
			t.Fatalf("expected ErrConfigNotFound, got %v", err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		err := repo.Delete(context.Background(), "default", "nope")
		if err != biz.ErrConfigNotFound {
			t.Fatalf("expected ErrConfigNotFound, got %v", err)
		}
	})
}

func TestMemoryRepository_List(t *testing.T) {
	repo := newMemoryRepository()
	_ = repo.Set(context.Background(), &biz.ConfigEntry{Namespace: "ns1", Key: "a", Value: "1", UpdatedAt: time.Now()})
	_ = repo.Set(context.Background(), &biz.ConfigEntry{Namespace: "ns1", Key: "b", Value: "2", UpdatedAt: time.Now()})

	entries, total, err := repo.List(context.Background(), "ns1", 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2, got %d", total)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestMemoryConfigRepositoryKeepsDistinctKeysAndIDs(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	first := &biz.ConfigEntry{Namespace: "a", Key: "b/c", Value: "first"}
	second := &biz.ConfigEntry{Namespace: "a/b", Key: "c", Value: "second"}
	require.NoError(t, repo.Set(ctx, first))
	require.NoError(t, repo.Set(ctx, second))
	got, err := repo.Get(ctx, first.Namespace, first.Key)
	require.NoError(t, err)
	require.Equal(t, first.Value, got.Value)
	require.NoError(t, repo.Delete(ctx, first.Namespace, first.Key))

	replacement := &biz.ConfigEntry{Namespace: first.Namespace, Key: first.Key}
	require.NoError(t, repo.Set(ctx, replacement))
	require.Greater(t, replacement.ID, second.ID)
	require.Greater(t, replacement.Revision, first.Revision)
	entries, total, err := repo.List(ctx, "", 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, entries, 1)
	require.Equal(t, replacement.ID, entries[0].ID)
}
