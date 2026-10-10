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

func TestHalfOpenProbeLeaseExpiresWithoutHealthReport(t *testing.T) {
	for _, kind := range []string{"channel", "account"} {
		t.Run(kind, func(t *testing.T) {
			channel, account := NewWeightedSelector(), NewSubscriptionAccountSelector()
			ch, ac := &Channel{ID: 1}, &SubscriptionAccount{ID: 1}
			channel.UpdateChannel(ch)
			account.RecordAccountHealth(1, false)
			channel.channels[1].circuitOpenUntil = time.Now().Add(-time.Second).UnixNano()
			account.accounts[1].circuitOpenUntil = time.Now().Add(-time.Second).UnixNano()
			selectProbe := func() error {
				if kind == "channel" {
					_, err := channel.Select(context.Background(), "g", []*Channel{ch})
					return err
				}
				_, err := account.Select(context.Background(), "g", []*SubscriptionAccount{ac})
				return err
			}
			if err := selectProbe(); err != nil {
				t.Fatal(err)
			}
			if err := selectProbe(); err == nil {
				t.Fatal("accepted another probe while the first probe is leased")
			}
			// No health report follows a local admission rejection or lost RPC.
			// Expire the lease without waiting for its wall-clock duration.
			if kind == "channel" {
				channel.channels[1].circuitProbeUntil = time.Now().Add(-time.Second).UnixNano()
			} else {
				account.accounts[1].circuitProbeUntil = time.Now().Add(-time.Second).UnixNano()
			}
			if err := selectProbe(); err != nil {
				t.Fatalf("source permanently locked after an unreported probe: %v", err)
			}
			if kind == "channel" && channel.channels[1].inflight.Load() != 0 {
				t.Fatal("unexecuted probe reserved an in-flight slot")
			}
		})
	}
}

func TestHalfOpenLeaseAndHealthDoNotReleaseExecutionSlot(t *testing.T) {
	s := NewWeightedSelector()
	ch := &Channel{ID: 1}
	s.UpdateChannel(ch)
	s.channels[1].circuitOpenUntil = time.Now().Add(-time.Second).UnixNano()
	if _, err := s.Select(context.Background(), "g", []*Channel{ch}); err != nil {
		t.Fatal(err)
	}
	s.RecordSlot(1, "execution", true)
	s.channels[1].circuitProbeUntil = time.Now().Add(-time.Second).UnixNano()
	if _, err := s.Select(context.Background(), "g", []*Channel{ch}); err != nil {
		t.Fatal(err)
	}
	s.RecordHealth(1, true, 1, "")
	if got := s.channels[1].inflight.Load(); got != 1 {
		t.Fatalf("lease expiry or health released a running execution: inflight=%d", got)
	}
	s.RecordSlot(1, "execution", false)
	if got := s.channels[1].inflight.Load(); got != 0 {
		t.Fatalf("completed execution retained its slot: inflight=%d", got)
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
