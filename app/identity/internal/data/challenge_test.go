package data

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"micro-one-api/app/identity/internal/biz"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func challengeRepositories(t *testing.T) (*miniredis.Miniredis, biz.ChallengeRepo, biz.ChallengeRepo) {
	t.Helper()
	srv := miniredis.RunT(t)
	newRepo := func() biz.ChallengeRepo {
		client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		return NewChallengeRepo(&Data{redis: client})
	}
	return srv, newRepo(), newRepo()
}

func TestChallengeCrossReplicaAtomicConsumption(t *testing.T) {
	_, first, second := challengeRepositories(t)
	ctx := context.Background()
	record := biz.EmailChallenge{UserID: 42, PasswordEpoch: 1234567890123456789}
	if err := first.Reserve(ctx, biz.ChallengePasswordReset, "test@example.com", "hash", record, time.Minute, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Consume(ctx, biz.ChallengePasswordReset, "test@example.com", "hash", 5); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("pending consumption = %v", err)
	}
	if err := first.Activate(ctx, biz.ChallengePasswordReset, "test@example.com", "hash"); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			got, err := second.Consume(ctx, biz.ChallengePasswordReset, "test@example.com", "hash", 5)
			if err == nil {
				winners.Add(1)
				if *got != record {
					t.Errorf("record = %+v", got)
				}
			} else if !errors.Is(err, biz.ErrChallengeInvalid) {
				t.Errorf("consume = %v", err)
			}
		})
	}
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("successful consumes = %d", got)
	}
}

func TestChallengeExpiryAttemptsAndReplacement(t *testing.T) {
	srv, first, second := challengeRepositories(t)
	ctx := context.Background()
	srv.SetTime(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	reserve := func(hash string) {
		t.Helper()
		if err := first.Reserve(ctx, biz.ChallengeVerification, "test@example.com", hash, biz.EmailChallenge{}, time.Minute, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := first.Activate(ctx, biz.ChallengeVerification, "test@example.com", hash); err != nil {
			t.Fatal(err)
		}
	}
	reserve("first")
	if err := second.Reserve(ctx, biz.ChallengeVerification, "test@example.com", "early", biz.EmailChallenge{}, time.Minute, 10*time.Second); !errors.Is(err, biz.ErrChallengeRateLimited) {
		t.Fatalf("cooldown = %v", err)
	}
	srv.SetTime(time.Date(2026, 10, 26, 0, 0, 11, 0, time.UTC))
	reserve("second")
	if err := first.Activate(ctx, biz.ChallengeVerification, "test@example.com", "first"); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("stale activation = %v", err)
	}
	if err := first.Cancel(ctx, biz.ChallengeVerification, "test@example.com", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Consume(ctx, biz.ChallengeVerification, "test@example.com", "first", 5); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("replaced = %v", err)
	}
	for range 4 {
		if _, err := second.Consume(ctx, biz.ChallengeVerification, "test@example.com", "wrong", 5); !errors.Is(err, biz.ErrChallengeInvalid) {
			t.Fatalf("wrong attempt = %v", err)
		}
	}
	if _, err := second.Consume(ctx, biz.ChallengeVerification, "test@example.com", "second", 5); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("exhausted = %v", err)
	}
	if err := first.Reserve(ctx, biz.ChallengeVerification, "test@example.com", "early", biz.EmailChallenge{}, time.Minute, 10*time.Second); !errors.Is(err, biz.ErrChallengeRateLimited) {
		t.Fatalf("attempt exhaustion bypassed resend interval: %v", err)
	}
	srv.SetTime(time.Date(2026, 10, 26, 0, 0, 22, 0, time.UTC))
	reserve("third")
	srv.FastForward(time.Minute)
	if _, err := second.Consume(ctx, biz.ChallengeVerification, "test@example.com", "third", 5); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("expired = %v", err)
	}
}

type challengeMailCapture struct {
	code           string
	fail           bool
	beforeDelivery func()
}

func (d *challengeMailCapture) DeliverVerificationCode(_ context.Context, _ string, code string) error {
	d.code = code
	if d.beforeDelivery != nil {
		d.beforeDelivery()
	}
	if d.fail {
		return biz.ErrMailUnavailable
	}
	return nil
}
func (d *challengeMailCapture) DeliverResetToken(ctx context.Context, email, code string) error {
	return d.DeliverVerificationCode(ctx, email, code)
}

func TestChallengeDeliveryFailureRevokesSecretAndAllowsRetry(t *testing.T) {
	srv, first, second := challengeRepositories(t)
	ctx := context.Background()
	deliverer := &challengeMailCapture{fail: true}
	one := biz.NewEmailChallengeUsecase(first, deliverer)
	two := biz.NewEmailChallengeUsecase(second, deliverer)
	deliverer.beforeDelivery = func() {
		for _, key := range srv.Keys() {
			value := srv.HGet(key, "hash")
			if value == deliverer.code || strings.Contains(key, "test@example.com") {
				t.Fatalf("secret or email stored verbatim")
			}
		}
		if _, err := two.Consume(ctx, biz.ChallengeVerification, "test@example.com", deliverer.code); !errors.Is(err, biz.ErrChallengeInvalid) {
			t.Fatalf("undelivered token = %v", err)
		}
	}
	if err := one.IssueVerification(ctx, " Test@Example.com "); !errors.Is(err, biz.ErrMailUnavailable) {
		t.Fatalf("failed delivery = %v", err)
	}
	failedCode := deliverer.code
	if _, err := two.Consume(ctx, biz.ChallengeVerification, "test@example.com", failedCode); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("failed code = %v", err)
	}
	deliverer.fail = false
	if err := one.IssueVerification(ctx, "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if deliverer.code == failedCode {
		t.Fatal("retry reused secret")
	}
	if _, err := two.Consume(ctx, biz.ChallengeVerification, "TEST@example.com", deliverer.code); err != nil {
		t.Fatal(err)
	}
}

func TestChallengeAvailabilityAndValidation(t *testing.T) {
	uc := biz.NewEmailChallengeUsecase(NewChallengeRepo(nil), &challengeMailCapture{})
	if uc.Available() {
		t.Fatal("missing Redis reported available")
	}
	if err := uc.IssueVerification(context.Background(), "test@example.com"); !errors.Is(err, biz.ErrChallengeUnavailable) {
		t.Fatal(err)
	}
	_, repo, _ := challengeRepositories(t)
	for _, input := range []string{"", "not an email", "Display <test@example.com>", "x@example.com\r\nBcc: x@example.net"} {
		if err := biz.NewEmailChallengeUsecase(repo, &challengeMailCapture{}).IssueVerification(context.Background(), input); !errors.Is(err, biz.ErrEmailInvalid) {
			t.Fatalf("%q = %v", input, err)
		}
	}
}

// Run against an isolated Redis server, never the application's Redis DB.
func TestChallengeRealRedisIntegration(t *testing.T) {
	address := os.Getenv("IDENTITY_CHALLENGE_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("set IDENTITY_CHALLENGE_REDIS_TEST_ADDR to an isolated Redis address")
	}
	ctx := context.Background()
	clientA := redis.NewClient(&redis.Options{Addr: address})
	clientB := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	if err := clientA.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	a, b := NewChallengeRepo(&Data{redis: clientA}), NewChallengeRepo(&Data{redis: clientB})
	email := fmt.Sprintf("a1-%d@example.test", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = clientA.Del(context.Background(), challengeKey(biz.ChallengePasswordReset, email), challengeKey(biz.ChallengeVerification, email)).Err()
	})
	if err := a.Reserve(ctx, biz.ChallengePasswordReset, email, "hash", biz.EmailChallenge{UserID: 7, PasswordEpoch: 1234567890123456789}, time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := b.Activate(ctx, biz.ChallengePasswordReset, email, "hash"); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			result, err := b.Consume(ctx, biz.ChallengePasswordReset, email, "hash", 5)
			if err == nil {
				winners.Add(1)
				if result.UserID != 7 || result.PasswordEpoch != 1234567890123456789 {
					t.Errorf("record precision lost: %+v", result)
				}
			} else if !errors.Is(err, biz.ErrChallengeInvalid) {
				t.Errorf("consume = %v", err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("replay winners = %d", winners.Load())
	}
	if err := a.Reserve(ctx, biz.ChallengePasswordReset, email, "new", biz.EmailChallenge{}, time.Second, time.Second); !errors.Is(err, biz.ErrChallengeRateLimited) {
		t.Fatalf("consumption bypassed cooldown: %v", err)
	}
	if err := a.Reserve(ctx, biz.ChallengeVerification, email, "short", biz.EmailChallenge{}, 20*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(ctx, biz.ChallengeVerification, email, "short"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := b.Consume(ctx, biz.ChallengeVerification, email, "short", 5); !errors.Is(err, biz.ErrChallengeInvalid) {
		t.Fatalf("Redis expiry = %v", err)
	}
}
