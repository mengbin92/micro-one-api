package biz

import (
	"context"
	"testing"
	"time"
)

func TestAccountPool_IsSchedulable_FiltersRuntimeBlockedAccount(t *testing.T) {
	ctx := context.Background()
	blocker := NewMemoryRuntimeBlocker()
	if err := blocker.Block(ctx, 7, time.Now().Add(time.Minute), "upstream 500"); err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	pool := NewAccountPool(blocker)

	if pool.IsSchedulable(ctx, &SubscriptionAccount{ID: 7}, time.Now()) {
		t.Fatal("blocked account should not be schedulable")
	}
	if !pool.IsSchedulable(ctx, &SubscriptionAccount{ID: 8}, time.Now()) {
		t.Fatal("unblocked account should be schedulable")
	}

	metrics := pool.Metrics()
	if metrics.Checked != 2 || metrics.RuntimeBlocked != 1 || metrics.Allowed != 1 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
