package biz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryTestRepo struct{ activated chan struct{} }

func (*recoveryTestRepo) Reserve(context.Context, string, string, string, EmailChallenge, time.Duration, time.Duration) error {
	return nil
}
func (r *recoveryTestRepo) Activate(context.Context, string, string, string) error {
	if r.activated != nil {
		r.activated <- struct{}{}
	}
	return nil
}
func (*recoveryTestRepo) Cancel(context.Context, string, string, string) error { return nil }
func (*recoveryTestRepo) Check(context.Context, string, string, string) (*EmailChallenge, error) {
	return nil, ErrChallengeInvalid
}
func (*recoveryTestRepo) Consume(context.Context, string, string, string, int) (*EmailChallenge, error) {
	return nil, ErrChallengeInvalid
}

type recoveryTestDelivery struct {
	deliver func(context.Context, string, string) error
}

func (d recoveryTestDelivery) DeliverVerificationCode(ctx context.Context, email, code string) error {
	return d.deliver(ctx, email, code)
}
func (d recoveryTestDelivery) DeliverResetToken(ctx context.Context, email, code string) error {
	return d.deliver(ctx, email, code)
}

func TestRecoveryReturnsBeforeSMTPAndBoundsConcurrentWork(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	defer close(release)
	var active, maxActive atomic.Int32
	uc := NewEmailChallengeUsecase(&recoveryTestRepo{}, recoveryTestDelivery{deliver: func(ctx context.Context, email, _ string) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		entered <- email
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	prepare := func(context.Context, string) (int64, int64, error) { return 7, 42, nil }
	for i := range 4 {
		result := make(chan error, 1)
		go func() {
			result <- uc.RequestRecovery(context.Background(), fmt.Sprintf("known-%d@example.test", i), prepare)
		}()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("public recovery waited for blocked SMTP")
		}
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("accepted recovery did not reach SMTP")
		}
	}
	if err := uc.RequestRecovery(context.Background(), "fifth@example.test", prepare); !errors.Is(err, ErrChallengeUnavailable) {
		t.Fatalf("unbounded work accepted: %v", err)
	}
	if got := maxActive.Load(); got != 4 {
		t.Fatalf("concurrent SMTP work = %d", got)
	}
}

func TestRecoveryKnownAndUnknownShareAcceptanceAndIgnoreRequestCancellation(t *testing.T) {
	prepareEntered := make(chan string, 2)
	releasePrepare := make(chan struct{})
	release := sync.OnceFunc(func() { close(releasePrepare) })
	defer release()
	delivered := make(chan string, 1)
	activated := make(chan struct{}, 1)
	uc := NewEmailChallengeUsecase(&recoveryTestRepo{activated: activated}, recoveryTestDelivery{deliver: func(ctx context.Context, email, _ string) error {
		if ctx.Err() != nil {
			t.Errorf("work inherited cancelled HTTP request: %v", ctx.Err())
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Error("recovery work has no bounded deadline")
		}
		delivered <- email
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	prepare := func(ctx context.Context, email string) (int64, int64, error) {
		prepareEntered <- email
		select {
		case <-releasePrepare:
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
		if email == "unknown@example.test" {
			return 0, 0, ErrUserNotFound
		}
		return 7, 42, nil
	}
	for _, email := range []string{"known@example.test", "unknown@example.test"} {
		result := make(chan error, 1)
		go func() { result <- uc.RequestRecovery(ctx, email, prepare) }()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("acceptance leaked eligibility: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("public acceptance waited for account lookup")
		}
	}
	for range 2 {
		select {
		case <-prepareEntered:
		case <-time.After(time.Second):
			t.Fatal("request was not accepted")
		}
	}
	cancel()
	// One release wakes both known and unknown workers without using the HTTP
	// request lifetime, then only the eligible address reaches mail delivery.
	release()
	select {
	case email := <-delivered:
		if email != "known@example.test" {
			t.Fatalf("mail sent to ineligible address")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled HTTP request lost accepted work")
	}
	select {
	case <-activated:
	case <-time.After(time.Second):
		t.Fatal("delivered challenge was not activated")
	}
}

func TestRecoveryRejectsInvalidInputWithoutLookingUpAccounts(t *testing.T) {
	var lookups atomic.Int32
	prepare := func(context.Context, string) (int64, int64, error) { lookups.Add(1); return 0, 0, nil }
	uc := NewEmailChallengeUsecase(&recoveryTestRepo{}, recoveryTestDelivery{deliver: func(context.Context, string, string) error { return nil }})
	if err := uc.RequestRecovery(context.Background(), "invalid", prepare); !errors.Is(err, ErrEmailInvalid) {
		t.Fatalf("invalid input = %v", err)
	}
	unavailable := NewEmailChallengeUsecase(nil, nil)
	if err := unavailable.RequestRecovery(context.Background(), "known@example.test", prepare); !errors.Is(err, ErrChallengeUnavailable) {
		t.Fatalf("missing config = %v", err)
	}
	if lookups.Load() != 0 {
		t.Fatal("rejected input looked up an account")
	}
}
