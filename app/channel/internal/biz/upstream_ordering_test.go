package biz

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"micro-one-api/domain/upstream/provider"
)

type delayedUpstreamSnapshotRepo struct {
	*mockChannelRepo
	mu           sync.Mutex
	channel      *Channel
	account      *SubscriptionAccount
	first        atomic.Bool
	started      chan struct{}
	release      chan struct{}
	unrestricted bool
}

func (r *delayedUpstreamSnapshotRepo) delayFirstRead() {
	if r.first.CompareAndSwap(false, true) {
		close(r.started)
		<-r.release
	}
}

func (r *delayedUpstreamSnapshotRepo) FindByID(context.Context, int64) (*Channel, error) {
	r.mu.Lock()
	channel := *r.channel
	r.mu.Unlock()
	r.delayFirstRead()
	return &channel, nil
}

func (r *delayedUpstreamSnapshotRepo) FindSubscriptionAccountByID(context.Context, int64) (*SubscriptionAccount, error) {
	r.mu.Lock()
	account := *r.account
	r.mu.Unlock()
	r.delayFirstRead()
	return &account, nil
}

func (r *delayedUpstreamSnapshotRepo) ListUnrestrictedChannelsByGroup(context.Context, string) ([]*Channel, error) {
	channel, err := r.FindByID(context.Background(), 7)
	return []*Channel{channel}, err
}

func TestOwnerSnapshotOrderingPreservesNewGenerationQuarantine(t *testing.T) {
	for _, path := range []string{"channel", "channel-excluding", "catch-all", "subscription", "subscription-excluding"} {
		t.Run(path, func(t *testing.T) {
			kind := "channel"
			if path == "subscription" || path == "subscription-excluding" {
				kind = "subscription"
			}
			uc, _, control, _ := upstreamExecutionFixture(t, kind)
			original := uc.repo.(*mockChannelRepo)
			repo := &delayedUpstreamSnapshotRepo{mockChannelRepo: original, channel: original.channels[7], account: original.accounts[7], started: make(chan struct{}), release: make(chan struct{})}
			uc.repo = repo
			selected := make(chan error, 1)
			go func() {
				var err error
				switch path {
				case "channel":
					_, err = uc.SelectChannel(context.Background(), "g", "model-a", false)
				case "channel-excluding":
					_, err = uc.SelectChannelExcluding(context.Background(), "g", "model-a", nil)
				case "catch-all":
					_, err = uc.SelectChannel(context.Background(), "g", "unlisted", false)
				case "subscription":
					_, err = uc.SelectSubscriptionAccount(context.Background(), "g", "model-a", "codex", false)
				default:
					_, err = uc.SelectSubscriptionAccountExcluding(context.Background(), "g", "model-a", "codex", nil)
				}
				selected <- err
			}()
			<-repo.started
			repo.mu.Lock()
			channel, account := *repo.channel, *repo.account
			channel.Key = "new-key"
			account.CredentialRevision++
			repo.channel, repo.account = &channel, &account
			repo.mu.Unlock()
			revision := channel.UpstreamRevision()
			if kind == "subscription" {
				revision = account.UpstreamRevision()
			}
			observed := make(chan error, 1)
			go func() {
				accepted, err := uc.RecordUpstreamObservation(context.Background(), provider.Observation{SourceKind: kind, SourceID: 7, ConfigurationRevision: revision, ID: "new-generation-quarantine", StartedAt: time.Now(), Outcome: provider.HealthNeutral, FailureKind: "configuration"})
				if err == nil && !accepted {
					err = ErrChannelNotFound
				}
				observed <- err
			}()
			// Without owner serialization the new observation completes first,
			// then the held old selection erases its configuration quarantine.
			var observationErr error
			observationFinished := false
			select {
			case observationErr = <-observed:
				observationFinished = true
			case <-time.After(20 * time.Millisecond):
			}
			close(repo.release)
			if err := <-selected; err != nil {
				t.Fatalf("initial selection failed: %v", err)
			}
			if !observationFinished {
				observationErr = <-observed
			}
			if observationErr != nil || control.revision != revision || !control.configurationBlocked {
				t.Fatalf("new quarantine rolled back: observation=%v revision=%s want=%s blocked=%t", observationErr, control.revision, revision, control.configurationBlocked)
			}
		})
	}
}

func TestOwnerExecutionClaimsApplyQuarantineToResolvedLegacyModels(t *testing.T) {
	for _, kind := range []string{"channel", "catch-all", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			channel := &Channel{ID: 7, Status: ChannelStatusEnabled, Group: "g", Models: []string{"legacy-a", "legacy-b"}, ModelMapping: `{"legacy-a":"actual-a","legacy-b":"actual-b"}`}
			account := &SubscriptionAccount{ID: 7, Status: ChannelStatusEnabled, Platform: "codex", Group: "g", Models: channel.Models, ModelMapping: channel.ModelMapping}
			repo := &mockChannelRepo{channels: map[int64]*Channel{7: channel}, accounts: map[int64]*SubscriptionAccount{7: account}, abilities: map[string][]Ability{}, accAbilities: map[string][]SubscriptionAccountAbility{}}
			if kind != "catch-all" {
				for _, model := range channel.Models {
					repo.abilities["g:"+model] = []Ability{{ChannelID: 7, Enabled: true}}
					repo.accAbilities["codex:g:"+model] = []SubscriptionAccountAbility{{AccountID: 7, Enabled: true}}
				}
			}
			uc := NewChannelUsecase(repo, nil)
			selectModel := func(model string) (string, error) {
				if kind == "subscription" {
					selected, err := uc.SelectSubscriptionAccount(context.Background(), "g", model, "codex", false)
					if err != nil {
						return "", err
					}
					actual := provider.ResolveModel(model, selected.ModelMapping, selected.UpstreamModelID, selected.Models)
					_, err = uc.ClaimUpstreamExecution(context.Background(), provider.ExecutionClaim{SourceKind: "subscription", SourceID: selected.ID, ConfigurationRevision: selected.UpstreamRevision(), UpstreamModelID: actual, ProbeID: selected.HealthProbeID})
					return actual, err
				}
				selected, err := uc.SelectChannel(context.Background(), "g", model, false)
				if err != nil {
					return "", err
				}
				actual := provider.ResolveModel(model, selected.ModelMapping, selected.UpstreamModelID, selected.Models)
				_, err = uc.ClaimUpstreamExecution(context.Background(), provider.ExecutionClaim{SourceKind: "channel", SourceID: selected.ID, ConfigurationRevision: selected.UpstreamRevision(), UpstreamModelID: actual, ProbeID: selected.HealthProbeID})
				return actual, err
			}
			if actual, err := selectModel("legacy-a"); err != nil || actual != "actual-a" {
				t.Fatalf("initial actual model=%q err=%v", actual, err)
			}
			sourceKind, revision := "channel", channel.UpstreamRevision()
			if kind == "subscription" {
				sourceKind, revision = "subscription", account.UpstreamRevision()
			}
			accepted, err := uc.RecordUpstreamObservation(context.Background(), provider.Observation{SourceKind: sourceKind, SourceID: 7, ConfigurationRevision: revision, ID: "model-failure", StartedAt: time.Now(), Outcome: provider.HealthNeutral, FailureKind: "model", UpstreamModelID: "actual-a"})
			if err != nil || !accepted {
				t.Fatalf("model quarantine not recorded: accepted=%t err=%v", accepted, err)
			}
			if _, err := selectModel("legacy-a"); err == nil {
				t.Fatal("empty legacy ability upstream ID bypassed quarantine")
			}
			if actual, err := selectModel("legacy-b"); err != nil || actual != "actual-b" {
				t.Fatalf("sibling model blocked: model=%q err=%v", actual, err)
			}
		})
	}
}

func TestOwnerExcludingAndCatchAllSelectionsRetainProbeIdentity(t *testing.T) {
	for _, path := range []string{"excluding", "catch-all"} {
		t.Run(path, func(t *testing.T) {
			uc, _, control, circuit := upstreamExecutionFixture(t, "channel")
			*circuit = time.Now().Add(-time.Second).UnixNano()
			var selected *Channel
			var err error
			if path == "excluding" {
				selected, err = uc.SelectChannelExcluding(context.Background(), "g", "model-a", nil)
			} else {
				selected, err = uc.SelectChannel(context.Background(), "g", "unlisted", false)
			}
			if err != nil || selected.HealthProbeID == "" || selected.HealthProbeID != control.probeID {
				t.Fatalf("selected source lost probe identity: %+v err=%v", selected, err)
			}
		})
	}
}
