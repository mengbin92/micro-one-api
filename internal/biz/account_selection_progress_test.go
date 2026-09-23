package biz

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type progressAccountClient struct {
	repeat bool
	calls  int
}

func (c *progressAccountClient) SelectSubscriptionAccount(context.Context, string, string, string, bool) (*SubscriptionAccount, error) {
	return nil, fmt.Errorf("excluding API required")
}
func (c *progressAccountClient) SelectSubscriptionAccountExcluding(_ context.Context, _, _, _ string, exclude map[int64]bool) (*SubscriptionAccount, error) {
	c.calls++
	for id := int64(1); id <= 9; id++ {
		if c.repeat || !exclude[id] {
			return &SubscriptionAccount{ID: id}, nil
		}
	}
	return nil, fmt.Errorf("no account")
}
func TestAccountSelectionPassesEightBlockedAccounts(t *testing.T) {
	ctx := context.Background()
	blocker := NewMemoryRuntimeBlocker()
	for id := int64(1); id <= 8; id++ {
		if err := blocker.Block(ctx, id, time.Now().Add(time.Hour), "test"); err != nil {
			t.Fatal(err)
		}
	}
	client := &progressAccountClient{}
	uc := &RelayUsecase{subscription: client, accountPool: NewAccountPool(blocker)}
	got, err := uc.selectSchedulableSubscriptionAccount(ctx, "g", "m", "", nil)
	if err != nil || got.ID != 9 {
		t.Fatalf("selection = %v, %v", got, err)
	}
	client.repeat = true
	if _, err = uc.selectSchedulableSubscriptionAccount(ctx, "g", "m", "", nil); err == nil {
		t.Fatal("repeated excluded source must terminate")
	}
}

func (c *progressAccountClient) GetSubscriptionAccountByID(context.Context, int64) (*SubscriptionAccount, error) {
	return nil, fmt.Errorf("not used")
}
