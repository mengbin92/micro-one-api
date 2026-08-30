package data

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRepositoryLoginRateLimiter(t *testing.T) {
	mr := miniredis.RunT(t)
	repo := &Repository{redis: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = repo.redis.Close() })
	ctx := context.Background()

	if err := repo.RecordLoginFailure(ctx, "user:alice", 5*time.Minute); err != nil {
		t.Fatalf("RecordLoginFailure() error = %v", err)
	}
	if err := repo.RecordLoginFailure(ctx, "user:alice", 5*time.Minute); err != nil {
		t.Fatalf("RecordLoginFailure() second error = %v", err)
	}
	count, err := repo.LoginFailureCount(ctx, "user:alice")
	if err != nil || count != 2 {
		t.Fatalf("LoginFailureCount() = %d, %v; want 2, nil", count, err)
	}
	if err := repo.ClearLoginFailures(ctx, "", "user:alice"); err != nil {
		t.Fatalf("ClearLoginFailures() error = %v", err)
	}
	count, err = repo.LoginFailureCount(ctx, "user:alice")
	if err != nil || count != 0 {
		t.Fatalf("LoginFailureCount() after clear = %d, %v; want 0, nil", count, err)
	}
}
