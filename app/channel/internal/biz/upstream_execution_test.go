package biz

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"micro-one-api/domain/routing"
	"micro-one-api/domain/upstream/provider"
)

func upstreamExecutionFixture(t *testing.T, kind string) (*ChannelUsecase, provider.ExecutionClaim, *upstreamControl, *int64) {
	t.Helper()
	channel := &Channel{ID: 7, Status: ChannelStatusEnabled, Key: "key", Group: "g", Models: []string{"model-a"}}
	account := &SubscriptionAccount{ID: 7, Status: ChannelStatusEnabled, CredentialRevision: 1, Platform: "codex", Group: "g", Models: []string{"model-a"}}
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{7: channel}, accounts: map[int64]*SubscriptionAccount{7: account},
		abilities:    map[string][]Ability{"g:model-a": {{ChannelID: 7, Enabled: true}}},
		accAbilities: map[string][]SubscriptionAccountAbility{"codex:g:model-a": {{AccountID: 7, Enabled: true}}},
	}
	uc := NewChannelUsecase(repo, nil)
	claim := provider.ExecutionClaim{SourceKind: kind, SourceID: 7, ConfigurationRevision: channel.UpstreamRevision(), UpstreamModelID: "model-a"}
	if kind == "subscription" {
		claim.ConfigurationRevision = account.UpstreamRevision()
	}
	if _, err := uc.ClaimUpstreamExecution(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if kind == "subscription" {
		state := uc.accountSelector.accounts[7]
		return uc, claim, &state.control, &state.circuitOpenUntil
	}
	state := uc.selector.channels[7]
	return uc, claim, &state.control, &state.circuitOpenUntil
}

func TestUpstreamExecutionClaimsEnforceControlsWithoutRevokingCatalogue(t *testing.T) {
	for _, kind := range []string{"channel", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			uc, claim, control, circuit := upstreamExecutionFixture(t, kind)
			ctx := context.Background()
			control.capacityUntil = time.Now().Add(time.Minute).UnixNano()
			if _, err := uc.ClaimUpstreamExecution(ctx, claim); !errors.Is(err, ErrUpstreamRateLimit) {
				t.Fatalf("capacity claim error=%v", err)
			}
			control.capacityUntil = 0
			control.models = map[string]int64{"model-a": time.Now().Add(time.Minute).UnixNano()}
			if _, err := uc.ClaimUpstreamExecution(ctx, claim); err == nil {
				t.Fatal("model quarantine admitted a pinned model")
			}
			sibling := claim
			sibling.UpstreamModelID = "model-b"
			if _, err := uc.ClaimUpstreamExecution(ctx, sibling); err != nil {
				t.Fatalf("model quarantine blocked sibling: %v", err)
			}
			control.configurationBlocked = true
			permission, err := uc.CanRoute(ctx, "g", "model-a", routing.Source{Kind: kind, ID: 7})
			if err != nil || !permission.Allowed {
				t.Fatalf("runtime controls revoked catalogue authorization: %+v %v", permission, err)
			}
			if _, err := uc.ClaimUpstreamExecution(ctx, sibling); err == nil {
				t.Fatal("configuration quarantine admitted pinned source")
			}
			control.configurationBlocked = false
			control.models = nil
			*circuit = time.Now().Add(time.Minute).UnixNano()
			if _, err := uc.ClaimUpstreamExecution(ctx, claim); err == nil {
				t.Fatal("open availability circuit admitted pinned source")
			}
		})
	}
}

func TestUpstreamExecutionClaimFencesConfigurationAndHalfOpenLease(t *testing.T) {
	for _, kind := range []string{"channel", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			uc, claim, control, circuit := upstreamExecutionFixture(t, kind)
			stale := claim
			stale.ConfigurationRevision = provider.ConfigurationRevision("old")
			if _, err := uc.ClaimUpstreamExecution(context.Background(), stale); err == nil || control.revision != claim.ConfigurationRevision {
				t.Fatalf("stale claim mutated owner configuration: err=%v revision=%s", err, control.revision)
			}
			*circuit = time.Now().Add(-time.Second).UnixNano()
			var accepted atomic.Int32
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					if _, err := uc.ClaimUpstreamExecution(context.Background(), claim); err == nil {
						accepted.Add(1)
					}
				})
			}
			wg.Wait()
			if accepted.Load() != 1 || control.probeID == "" {
				t.Fatalf("half-open admitted %d unleased claims, probe=%q", accepted.Load(), control.probeID)
			}
			claim.ProbeID = control.probeID
			lease, err := uc.ClaimUpstreamExecution(context.Background(), claim)
			if err != nil || lease.ProbeID != claim.ProbeID || lease.ConfigurationRevision != claim.ConfigurationRevision {
				t.Fatalf("matching selected lease rejected: %+v %v", lease, err)
			}
			claim.ProbeID = "other-probe"
			if _, err := uc.ClaimUpstreamExecution(context.Background(), claim); err == nil {
				t.Fatal("unrelated request reused active probe")
			}
		})
	}
}
