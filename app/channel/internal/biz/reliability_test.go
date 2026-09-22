package biz

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLowTrafficConsecutiveFailuresAcrossWindows(t *testing.T) {
	channel := NewWeightedSelector()
	channel.UpdateChannel(&Channel{ID: 1})
	account := NewSubscriptionAccountSelector()
	for i := 0; i < 5; i++ {
		channel.RecordHealth(1, false, 1, "failure")
		account.RecordAccountHealth(1, false)
		// Empty the time window, but preserve the logical failure streak.
		channel.channels[1].recentErrors = NewSlidingCounter(time.Minute)
		account.accounts[1].recentErrors = NewSlidingCounter(time.Minute)
	}
	if channel.channels[1].circuitOpenUntil == 0 || account.accounts[1].circuitOpenUntil == 0 {
		t.Fatal("five low-traffic failures did not isolate both source kinds")
	}
}

func TestHalfOpenAdmitsOneConcurrentProbe(t *testing.T) {
	for _, kind := range []string{"channel", "account"} {
		t.Run(kind, func(t *testing.T) {
			channel, account := NewWeightedSelector(), NewSubscriptionAccountSelector()
			ch, ac := &Channel{ID: 1}, &SubscriptionAccount{ID: 1}
			channel.UpdateChannel(ch)
			account.RecordAccountHealth(1, false)
			channel.channels[1].circuitOpenUntil = time.Now().Add(-time.Second).UnixNano()
			account.accounts[1].circuitOpenUntil = time.Now().Add(-time.Second).UnixNano()
			var accepted atomic.Int32
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					var err error
					if kind == "channel" {
						_, err = channel.Select(context.Background(), "g", []*Channel{ch})
					} else {
						_, err = account.Select(context.Background(), "g", []*SubscriptionAccount{ac})
					}
					if err == nil {
						accepted.Add(1)
					}
				})
			}
			wg.Wait()
			if accepted.Load() != 1 {
				t.Fatalf("accepted %d probes, want 1", accepted.Load())
			}
		})
	}
}

func TestSuccessfulProbeClearsFailureWindow(t *testing.T) {
	channel, account := NewWeightedSelector(), NewSubscriptionAccountSelector()
	channel.UpdateChannel(&Channel{ID: 1})
	for range 12 {
		channel.RecordHealth(1, false, 1, "")
		account.RecordAccountHealth(1, false)
	}
	channel.channels[1].circuitOpenUntil = circuitHalfOpenSentinel
	account.accounts[1].circuitOpenUntil = circuitHalfOpenSentinel
	channel.RecordHealth(1, true, 1, "")
	account.RecordAccountHealth(1, true)
	if channel.channels[1].circuitOpenUntil != 0 || account.accounts[1].circuitOpenUntil != 0 {
		t.Fatal("successful probe immediately retripped on old failures")
	}
}

func TestConsecutiveFailuresResetOnSuccess(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	for range 4 {
		s.RecordAccountHealth(1, false)
	}
	s.RecordAccountHealth(1, true)
	s.accounts[1].recentErrors = NewSlidingCounter(time.Minute)
	for range 4 {
		s.RecordAccountHealth(1, false)
	}
	if s.accounts[1].circuitOpenUntil != 0 {
		t.Fatal("success did not reset streak")
	}
	s.RecordAccountHealth(1, false)
	if s.accounts[1].circuitOpenUntil == 0 {
		t.Fatal("fifth failure did not open")
	}
}
func TestSelectorFailureThresholdValidation(t *testing.T) {
	for _, value := range []string{"0", "-1", "no"} {
		t.Setenv("CHANNEL_SELECTOR_CONSECUTIVE_FAILURES", value)
		if _, err := SelectorFailureThresholdFromEnv(); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	t.Setenv("CHANNEL_SELECTOR_CONSECUTIVE_FAILURES", "7")
	n, err := SelectorFailureThresholdFromEnv()
	if err != nil || n != 7 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
