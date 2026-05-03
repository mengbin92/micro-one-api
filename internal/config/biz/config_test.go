package biz

import (
	"context"
	"testing"
	"time"
)

type mockConfigRepo struct {
	entries map[string]*ConfigEntry // key: "namespace/key"
	getErr  error
	setErr  error
	delErr  error
	listErr error
}

func (m *mockConfigRepo) Get(ctx context.Context, namespace, key string) (*ConfigEntry, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	e, ok := m.entries[namespace+"/"+key]
	if !ok {
		return nil, ErrConfigNotFound
	}
	return e, nil
}

func (m *mockConfigRepo) List(ctx context.Context, namespace string, page, pageSize int32) ([]*ConfigEntry, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	var result []*ConfigEntry
	for _, e := range m.entries {
		if e.Namespace == namespace {
			result = append(result, e)
		}
	}
	total := int64(len(result))
	start := int((page - 1) * pageSize)
	if start >= len(result) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(result) {
		end = len(result)
	}
	return result[start:end], total, nil
}

func (m *mockConfigRepo) Set(ctx context.Context, entry *ConfigEntry) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.entries[entry.Namespace+"/"+entry.Key] = entry
	return nil
}

func (m *mockConfigRepo) Delete(ctx context.Context, namespace, key string) error {
	if m.delErr != nil {
		return m.delErr
	}
	k := namespace + "/" + key
	if _, ok := m.entries[k]; !ok {
		return ErrConfigNotFound
	}
	delete(m.entries, k)
	return nil
}

func newMockRepo(entries ...*ConfigEntry) *mockConfigRepo {
	m := &mockConfigRepo{entries: make(map[string]*ConfigEntry)}
	for _, e := range entries {
		m.entries[e.Namespace+"/"+e.Key] = e
	}
	return m
}

func TestConfigUsecase_GetConfig(t *testing.T) {
	repo := newMockRepo(&ConfigEntry{Namespace: "default", Key: "theme", Value: "dark"})
	uc := NewConfigUsecase(repo, nil)

	t.Run("success", func(t *testing.T) {
		e, err := uc.GetConfig(context.Background(), "default", "theme")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Value != "dark" {
			t.Fatalf("expected dark, got %s", e.Value)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := uc.GetConfig(context.Background(), "default", "missing")
		if err != ErrConfigNotFound {
			t.Fatalf("expected ErrConfigNotFound, got %v", err)
		}
	})

	t.Run("empty key", func(t *testing.T) {
		_, err := uc.GetConfig(context.Background(), "default", "")
		if err != ErrInvalidKey {
			t.Fatalf("expected ErrInvalidKey, got %v", err)
		}
	})
}

func TestConfigUsecase_SetConfig(t *testing.T) {
	repo := newMockRepo()
	uc := NewConfigUsecase(repo, nil)

	t.Run("create new", func(t *testing.T) {
		err := uc.SetConfig(context.Background(), "default", "theme", "dark", "UI theme")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		e, _ := repo.Get(context.Background(), "default", "theme")
		if e.Value != "dark" || e.Comment != "UI theme" {
			t.Fatalf("unexpected entry: %+v", e)
		}
		if e.UpdatedAt.IsZero() {
			t.Fatal("expected UpdatedAt to be set")
		}
	})

	t.Run("update existing", func(t *testing.T) {
		err := uc.SetConfig(context.Background(), "default", "theme", "light", "updated")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		e, _ := repo.Get(context.Background(), "default", "theme")
		if e.Value != "light" {
			t.Fatalf("expected light, got %s", e.Value)
		}
	})

	t.Run("empty key", func(t *testing.T) {
		err := uc.SetConfig(context.Background(), "default", "", "val", "")
		if err != ErrInvalidKey {
			t.Fatalf("expected ErrInvalidKey, got %v", err)
		}
	})
}

func TestConfigUsecase_DeleteConfig(t *testing.T) {
	repo := newMockRepo(&ConfigEntry{Namespace: "default", Key: "theme", Value: "dark"})
	uc := NewConfigUsecase(repo, nil)

	t.Run("success", func(t *testing.T) {
		err := uc.DeleteConfig(context.Background(), "default", "theme")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = repo.Get(context.Background(), "default", "theme")
		if err != ErrConfigNotFound {
			t.Fatalf("expected ErrConfigNotFound after delete, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := uc.DeleteConfig(context.Background(), "default", "missing")
		if err != ErrConfigNotFound {
			t.Fatalf("expected ErrConfigNotFound, got %v", err)
		}
	})

	t.Run("empty key", func(t *testing.T) {
		err := uc.DeleteConfig(context.Background(), "default", "")
		if err != ErrInvalidKey {
			t.Fatalf("expected ErrInvalidKey, got %v", err)
		}
	})
}

func TestConfigUsecase_ListConfigs(t *testing.T) {
	repo := newMockRepo(
		&ConfigEntry{Namespace: "default", Key: "a", Value: "1"},
		&ConfigEntry{Namespace: "default", Key: "b", Value: "2"},
		&ConfigEntry{Namespace: "other", Key: "c", Value: "3"},
	)
	uc := NewConfigUsecase(repo, nil)

	t.Run("filters by namespace", func(t *testing.T) {
		entries, total, err := uc.ListConfigs(context.Background(), "default", 1, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	})

	t.Run("normalizes page", func(t *testing.T) {
		_, total, err := uc.ListConfigs(context.Background(), "default", 0, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
	})

	t.Run("normalizes pageSize", func(t *testing.T) {
		_, _, err := uc.ListConfigs(context.Background(), "default", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("page beyond data", func(t *testing.T) {
		entries, total, err := uc.ListConfigs(context.Background(), "default", 100, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		if len(entries) != 0 {
			t.Fatalf("expected 0 entries, got %d", len(entries))
		}
	})
}

func TestConfigEntry_Fields(t *testing.T) {
	now := time.Now()
	e := &ConfigEntry{
		ID:        1,
		Namespace: "ns",
		Key:       "k",
		Value:     "v",
		Comment:   "c",
		UpdatedAt: now,
	}
	if e.ID != 1 || e.Namespace != "ns" || e.Key != "k" || e.Value != "v" || e.Comment != "c" {
		t.Fatalf("unexpected fields: %+v", e)
	}
}
