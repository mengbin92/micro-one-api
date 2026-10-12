package biz

import (
	"context"
	"fmt"
	"testing"
	"time"

	"micro-one-api/domain/upstream/provider"
)

type upstreamSelectorFixture struct {
	kind    string
	channel *WeightedSelector
	account *SubscriptionAccountSelector
	ch      Channel
	ac      SubscriptionAccount
	nextID  int
}

type retryableHealthRepo struct {
	*mockChannelRepo
	writeErr error
}

func (r *retryableHealthRepo) RecordHealth(ctx context.Context, event ChannelHealthEvent, threshold int32, cooldown time.Duration) (*Channel, error) {
	if r.writeErr != nil {
		err := r.writeErr
		r.writeErr = nil
		return nil, err
	}
	return r.mockChannelRepo.RecordHealth(ctx, event, threshold, cooldown)
}

func testUpstreamSelectors(t *testing.T, test func(*testing.T, *upstreamSelectorFixture)) {
	t.Helper()
	for _, kind := range []string{"channel", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			f := &upstreamSelectorFixture{kind: kind, channel: NewWeightedSelector(), account: NewSubscriptionAccountSelector(), ch: Channel{ID: 1}, ac: SubscriptionAccount{ID: 1}}
			if _, err := f.selectSource("model-a"); err != nil {
				t.Fatal(err)
			}
			test(t, f)
		})
	}
}

func (f *upstreamSelectorFixture) selectSource(model string) (string, error) {
	if f.kind == "channel" {
		ch := f.ch
		ch.UpstreamModelID = model
		selected, err := f.channel.Select(context.Background(), "g", []*Channel{&ch})
		if err != nil {
			return "", err
		}
		return selected.HealthProbeID, nil
	}
	ac := f.ac
	ac.UpstreamModelID = model
	selected, err := f.account.Select(context.Background(), "g", []*SubscriptionAccount{&ac})
	if err != nil {
		return "", err
	}
	return selected.HealthProbeID, nil
}

func (f *upstreamSelectorFixture) observation(outcome provider.HealthOutcome, failureKind string) provider.Observation {
	f.nextID++
	revision := f.ch.UpstreamRevision()
	if f.kind == "subscription" {
		revision = f.ac.UpstreamRevision()
	}
	return provider.Observation{ID: fmt.Sprint(f.nextID), SourceKind: f.kind, SourceID: 1, ConfigurationRevision: revision, StartedAt: time.Now(), Outcome: outcome, FailureKind: failureKind, UpstreamModelID: "model-a"}
}

func (f *upstreamSelectorFixture) record(o provider.Observation) bool {
	if f.kind == "channel" {
		return f.channel.RecordObservation(o)
	}
	return f.account.RecordObservation(o)
}

func (f *upstreamSelectorFixture) recordSlot(id string, acquired bool) bool {
	if f.kind == "channel" {
		return f.channel.RecordSlot(1, id, acquired)
	}
	return f.account.RecordSlot(1, id, acquired)
}

func (f *upstreamSelectorFixture) control() *upstreamControl {
	if f.kind == "channel" {
		return &f.channel.channels[1].control
	}
	return &f.account.accounts[1].control
}

func (f *upstreamSelectorFixture) circuit() *int64 {
	if f.kind == "channel" {
		return &f.channel.channels[1].circuitOpenUntil
	}
	return &f.account.accounts[1].circuitOpenUntil
}

func (f *upstreamSelectorFixture) lease() *int64 {
	if f.kind == "channel" {
		return &f.channel.channels[1].circuitProbeUntil
	}
	return &f.account.accounts[1].circuitProbeUntil
}

func (f *upstreamSelectorFixture) health() (int, int) {
	if f.kind == "channel" {
		state := f.channel.channels[1]
		return state.recentErrors.Total(), state.consecutiveFailures
	}
	state := f.account.accounts[1]
	return state.recentErrors.Total(), state.consecutiveFailures
}

func (f *upstreamSelectorFixture) effectiveWeight() int64 {
	if f.kind == "channel" {
		return channelEffectiveWeight(f.channel.channels[1])
	}
	return accountEffectiveWeight(f.account.accounts[1])
}

func (f *upstreamSelectorFixture) legacySuccess() {
	if f.kind == "channel" {
		f.channel.RecordHealth(1, true, 1, "")
	} else {
		f.account.RecordAccountHealth(1, true)
	}
}

func (f *upstreamSelectorFixture) openProbe(t *testing.T) string {
	t.Helper()
	for range 5 {
		if !f.record(f.observation(provider.HealthFailure, "")) {
			t.Fatal("availability failure rejected")
		}
	}
	if *f.circuit() <= time.Now().UnixNano() {
		t.Fatal("consecutive availability failures did not open circuit")
	}
	*f.circuit() = time.Now().Add(-time.Second).UnixNano()
	probeID, err := f.selectSource("model-a")
	if err != nil || probeID == "" {
		t.Fatalf("half-open probe ID=%q err=%v", probeID, err)
	}
	return probeID
}

func TestUpstreamCapacityDoesNotAffectAvailability(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		if !f.record(f.observation(provider.HealthFailure, "")) {
			t.Fatal("availability failure rejected")
		}
		for range 20 {
			o := f.observation(provider.HealthNeutral, "capacity")
			o.RetryAfter = 2 * time.Minute
			if !f.record(o) {
				t.Fatal("capacity observation rejected")
			}
		}
		if total, failures := f.health(); total != 1 || failures != 1 || *f.circuit() != 0 {
			t.Fatalf("capacity polluted availability: total=%d failures=%d circuit=%d", total, failures, *f.circuit())
		}
		if _, err := f.selectSource("model-a"); err != ErrUpstreamRateLimit {
			t.Fatalf("capacity rejection=%v, want ErrUpstreamRateLimit", err)
		}
		f.control().capacityUntil = time.Now().Add(-time.Second).UnixNano()
		if _, err := f.selectSource("model-a"); err != nil {
			t.Fatalf("capacity cooldown did not release source: %v", err)
		}
	})
}

func TestUpstreamConfigurationIsolationClearsOnlyOnRevisionChange(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		old := f.observation(provider.HealthNeutral, "configuration")
		if !f.record(old) {
			t.Fatal("configuration observation rejected")
		}
		if _, err := f.selectSource("model-a"); err == nil {
			t.Fatal("misconfigured source remained eligible")
		}
		if !f.record(f.observation(provider.HealthSuccess, "")) {
			t.Fatal("successful observation rejected")
		}
		if _, err := f.selectSource("model-a"); err == nil {
			t.Fatal("successful request cleared configuration isolation")
		}
		f.ch.Key = "rotated"
		f.ac.CredentialRevision++
		if _, err := f.selectSource("model-a"); err != nil {
			t.Fatalf("rotated configuration remained isolated: %v", err)
		}
		old.ID = "late-configuration"
		if f.record(old) {
			t.Fatal("old configuration generation was accepted")
		}
		if _, err := f.selectSource("model-a"); err != nil {
			t.Fatalf("old configuration report isolated new credentials: %v", err)
		}
	})
}

func TestUpstreamModelIsolationIsExact(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		if !f.record(f.observation(provider.HealthNeutral, "model")) {
			t.Fatal("model failure rejected")
		}
		if _, err := f.selectSource("model-a"); err == nil {
			t.Fatal("unavailable upstream model remained eligible")
		}
		if _, err := f.selectSource("model-b"); err != nil {
			t.Fatalf("model failure isolated healthy sibling model: %v", err)
		}
		if total, failures := f.health(); total != 0 || failures != 0 {
			t.Fatalf("model failure polluted availability: total=%d failures=%d", total, failures)
		}
		f.control().models["model-a"] = time.Now().Add(-time.Second).UnixNano()
		if _, err := f.selectSource("model-a"); err != nil {
			t.Fatalf("model cooldown did not release model: %v", err)
		}
	})
}

func TestUpstreamObservationDuplicateIsIgnored(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		o := f.observation(provider.HealthFailure, "")
		if !f.record(o) || f.record(o) {
			t.Fatal("one observation must be applied exactly once")
		}
		if total, failures := f.health(); total != 1 || failures != 1 {
			t.Fatalf("duplicate polluted availability: total=%d failures=%d", total, failures)
		}
	})
}

func TestUpstreamRecoveryRequiresMatchingProbe(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		stale := f.observation(provider.HealthSuccess, "")
		probeID := f.openProbe(t)
		if f.record(stale) {
			t.Fatal("pre-incident observation accepted")
		}
		for _, token := range []string{"", "wrong-probe"} {
			o := f.observation(provider.HealthSuccess, "")
			o.ProbeID = token
			if f.record(o) {
				t.Fatalf("unrelated probe %q accepted", token)
			}
		}
		f.legacySuccess()
		if *f.circuit() == 0 {
			t.Fatal("unfenced health report closed half-open circuit")
		}
		o := f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if !f.record(o) || *f.circuit() != 0 {
			t.Fatal("matching business probe did not close circuit")
		}
		if f.record(o) {
			t.Fatal("completed probe was accepted twice")
		}
	})
}

func TestUpstreamIndependentProbeCannotRecoverAvailability(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		probeID := f.openProbe(t)
		o := f.observation(provider.HealthSuccess, "probe")
		o.ProbeID = probeID
		if f.record(o) || *f.circuit() == 0 {
			t.Fatal("independent endpoint probe closed availability circuit")
		}
	})
}

func TestUpstreamUnleasedHealthCannotRecoverAvailability(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		for range 5 {
			if !f.record(f.observation(provider.HealthFailure, "")) {
				t.Fatal("availability failure rejected")
			}
		}
		*f.circuit() = time.Now().Add(-time.Second).UnixNano()
		f.legacySuccess()
		if *f.circuit() == 0 {
			t.Fatal("health report without an admitted probe closed availability circuit")
		}
	})
}

func TestUpstreamExpiredProbeCannotRecoverAvailability(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		probeID := f.openProbe(t)
		*f.lease() = time.Now().Add(-time.Second).UnixNano()
		o := f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if f.record(o) || *f.circuit() == 0 {
			t.Fatal("expired probe closed availability circuit")
		}
		newProbeID, err := f.selectSource("model-a")
		if err != nil || newProbeID == "" || newProbeID == probeID {
			t.Fatalf("lost probe lease did not yield a fresh token: old=%q new=%q err=%v", probeID, newProbeID, err)
		}
		o.ID = "late-old-probe"
		if f.record(o) {
			t.Fatal("replaced probe accepted")
		}
		o = f.observation(provider.HealthSuccess, "")
		o.ProbeID = newProbeID
		if !f.record(o) || *f.circuit() != 0 {
			t.Fatal("new probe did not recover source")
		}
	})
}

func TestUpstreamNeutralProbeReleasesLeaseWithoutRecovering(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		probeID := f.openProbe(t)
		o := f.observation(provider.HealthNeutral, "")
		o.ProbeID = probeID
		if !f.record(o) || *f.circuit() == 0 {
			t.Fatal("neutral probe must release admission without closing circuit")
		}
		if total, failures := f.health(); total != 5 || failures != 5 {
			t.Fatalf("neutral probe polluted availability: total=%d failures=%d", total, failures)
		}
		o = f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if f.record(o) {
			t.Fatal("neutral probe's released token remained valid")
		}
		newProbeID, err := f.selectSource("model-a")
		if err != nil || newProbeID == "" || newProbeID == probeID {
			t.Fatalf("neutral probe did not permit a fresh probe: ID=%q err=%v", newProbeID, err)
		}
	})
}

func TestUpstreamCapacityProbeReleasesLeaseAndKeepsCooldown(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		probeID := f.openProbe(t)
		o := f.observation(provider.HealthNeutral, "capacity")
		o.ProbeID = probeID
		if !f.record(o) || *f.circuit() == 0 {
			t.Fatal("capacity probe must preserve availability circuit")
		}
		if _, err := f.selectSource("model-a"); err != ErrUpstreamRateLimit {
			t.Fatalf("capacity probe lost cooldown: %v", err)
		}
		f.control().capacityUntil = time.Now().Add(-time.Second).UnixNano()
		newProbeID, err := f.selectSource("model-a")
		if err != nil || newProbeID == "" || newProbeID == probeID {
			t.Fatalf("capacity cooldown did not allow new recovery probe: ID=%q err=%v", newProbeID, err)
		}
		if total, failures := f.health(); total != 5 || failures != 5 {
			t.Fatalf("capacity probe polluted availability: total=%d failures=%d", total, failures)
		}
	})
}

func TestUpstreamRecoveryRampsOnlyOnVerifiedSuccesses(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		fullWeight := f.effectiveWeight()
		probeID := f.openProbe(t)
		o := f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if !f.record(o) || *f.circuit() != 0 {
			t.Fatal("matching probe did not start recovery")
		}
		for verifiedSuccesses := int64(1); verifiedSuccesses <= 5; verifiedSuccesses++ {
			want := fullWeight * verifiedSuccesses / 5
			if got := f.effectiveWeight(); got != want || got <= 0 {
				t.Fatalf("weight after %d verified successes=%d, want %d", verifiedSuccesses, got, want)
			}
			f.legacySuccess()
			if got := f.effectiveWeight(); got != want {
				t.Fatalf("unfenced health advanced recovery weight to %d, want %d", got, want)
			}
			if f.record(f.observation(provider.HealthSuccess, "probe")) || f.effectiveWeight() != want {
				t.Fatal("independent endpoint probe advanced business recovery")
			}
			for _, failureKind := range []string{"", "capacity"} {
				if !f.record(f.observation(provider.HealthNeutral, failureKind)) {
					t.Fatal("neutral observation rejected")
				}
				if got := f.effectiveWeight(); got != want {
					t.Fatalf("neutral %q advanced recovery weight to %d, want %d", failureKind, got, want)
				}
			}
			f.control().capacityUntil = time.Now().Add(-time.Second).UnixNano()
			if _, err := f.selectSource("model-a"); err != nil {
				t.Fatalf("positive recovery weight did not admit business traffic: %v", err)
			}
			if !f.record(f.observation(provider.HealthSuccess, "")) {
				t.Fatal("verified success rejected")
			}
		}
		if got := f.effectiveWeight(); got != fullWeight {
			t.Fatalf("completed recovery exceeded configured weight: got=%d want=%d", got, fullWeight)
		}
	})
}

func TestUpstreamRecoveryFailureReopensAndRestartsRamp(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		fullWeight := f.effectiveWeight()
		probeID := f.openProbe(t)
		o := f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if !f.record(o) || !f.record(f.observation(provider.HealthSuccess, "")) {
			t.Fatal("verified recovery successes rejected")
		}
		if !f.record(f.observation(provider.HealthFailure, "")) {
			t.Fatal("recovery availability failure rejected")
		}
		if *f.circuit() <= time.Now().UnixNano() {
			t.Fatal("first verified availability failure during recovery did not reopen circuit")
		}
		*f.circuit() = time.Now().Add(-time.Second).UnixNano()
		newProbeID, err := f.selectSource("model-a")
		if err != nil || newProbeID == "" || newProbeID == probeID {
			t.Fatalf("failed recovery did not yield fresh probe: ID=%q err=%v", newProbeID, err)
		}
		o = f.observation(provider.HealthSuccess, "")
		o.ProbeID = newProbeID
		if !f.record(o) {
			t.Fatal("matching new recovery probe rejected")
		}
		if got := f.effectiveWeight(); got != fullWeight/5 {
			t.Fatalf("recovery resumed old ramp instead of restarting: got=%d want=%d", got, fullWeight/5)
		}
	})
}

func TestUpstreamCompletedRecoveryRestoresNormalFailureThreshold(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		fullWeight := f.effectiveWeight()
		probeID := f.openProbe(t)
		o := f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if !f.record(o) {
			t.Fatal("matching recovery probe rejected")
		}
		for range 4 {
			if !f.record(f.observation(provider.HealthSuccess, "")) {
				t.Fatal("verified recovery success rejected")
			}
		}
		if f.effectiveWeight() != fullWeight {
			t.Fatal("five verified successes did not restore full weight")
		}
		for failure := 1; failure <= 5; failure++ {
			if !f.record(f.observation(provider.HealthFailure, "")) {
				t.Fatal("post-recovery availability failure rejected")
			}
			if failure < 5 && *f.circuit() != 0 {
				t.Fatalf("completed recovery retained ramp failure policy: opened after %d failures", failure)
			}
		}
		if *f.circuit() <= time.Now().UnixNano() {
			t.Fatal("completed recovery disabled ordinary consecutive-failure breaker")
		}
	})
}

func TestUpstreamExecutionRenewalExtendsOnlyCurrentProbe(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		probeID := f.openProbe(t)
		if !f.recordSlot(probeID, true) {
			t.Fatal("probe execution slot rejected")
		}
		*f.lease() = time.Now().Add(time.Second).UnixNano()
		deadline := *f.lease()
		if !f.recordSlot("unrelated-request", true) || *f.lease() != deadline {
			t.Fatal("unrelated execution renewed probe lease")
		}
		if !f.recordSlot(probeID, true) || *f.lease() < time.Now().Add(executionSlotLeaseDuration-time.Second).UnixNano() {
			t.Fatal("matching live execution did not extend probe lease")
		}
		*f.lease() = time.Now().Add(-time.Second).UnixNano()
		deadline = *f.lease()
		if !f.recordSlot(probeID, true) || *f.lease() != deadline {
			t.Fatal("delayed execution renewal resurrected expired probe authority")
		}
		newProbeID, err := f.selectSource("model-a")
		if err != nil || newProbeID == "" || newProbeID == probeID {
			t.Fatalf("expired probe did not yield a fresh identity: ID=%q err=%v", newProbeID, err)
		}
		*f.lease() = time.Now().Add(time.Second).UnixNano()
		deadline = *f.lease()
		if !f.recordSlot(probeID, true) || *f.lease() != deadline {
			t.Fatal("old execution renewed the new probe lease")
		}
		if !f.recordSlot(newProbeID, true) || *f.lease() < time.Now().Add(executionSlotLeaseDuration-time.Second).UnixNano() {
			t.Fatal("new matching execution did not extend probe lease")
		}
	})
}

func TestUpstreamFailedProbeFencesPreviousIncident(t *testing.T) {
	testUpstreamSelectors(t, func(t *testing.T, f *upstreamSelectorFixture) {
		probeID := f.openProbe(t)
		stale := f.observation(provider.HealthNeutral, "capacity")
		stale.StartedAt = time.Unix(0, f.control().incidentAt+1)
		o := f.observation(provider.HealthFailure, "")
		o.ProbeID = probeID
		if !f.record(o) || *f.circuit() <= time.Now().UnixNano() {
			t.Fatal("failed business probe did not reopen circuit")
		}
		if f.record(stale) {
			t.Fatalf("previous incident observation altered reopened circuit controls: started=%d incident=%d", stale.StartedAt.UnixNano(), f.control().incidentAt)
		}
		*f.circuit() = time.Now().Add(-time.Second).UnixNano()
		newProbeID, err := f.selectSource("model-a")
		if err != nil || newProbeID == "" || newProbeID == probeID {
			t.Fatalf("reopened circuit did not issue a fresh probe: ID=%q err=%v", newProbeID, err)
		}
		o = f.observation(provider.HealthSuccess, "")
		o.ProbeID = probeID
		if f.record(o) {
			t.Fatal("previous incident probe recovered new incident")
		}
		o.ProbeID = newProbeID
		if !f.record(o) || *f.circuit() != 0 {
			t.Fatal("new incident probe did not recover source")
		}
	})
}

func TestChannelUsecaseNeutralAndStaleObservationsDoNotWriteHealth(t *testing.T) {
	channel := &Channel{ID: 1, Key: "old", HealthConsecutiveFailures: 2, HealthStatus: ChannelHealthDegraded}
	uc := NewChannelUsecase(&mockChannelRepo{channels: map[int64]*Channel{1: channel}}, nil)
	o := provider.Observation{ID: "capacity", SourceKind: "channel", SourceID: 1, ConfigurationRevision: channel.UpstreamRevision(), StartedAt: time.Now(), Outcome: provider.HealthNeutral, FailureKind: "capacity"}
	accepted, err := uc.RecordUpstreamObservation(context.Background(), o)
	if err != nil || !accepted {
		t.Fatalf("neutral observation accepted=%v err=%v", accepted, err)
	}
	if channel.HealthConsecutiveFailures != 2 || channel.HealthStatus != ChannelHealthDegraded {
		t.Fatal("neutral observation wrote persistent availability health")
	}
	channel.Key = "rotated"
	o.ID = "old-success"
	o.Outcome = provider.HealthSuccess
	o.FailureKind = ""
	accepted, err = uc.RecordUpstreamObservation(context.Background(), o)
	if err != nil || accepted {
		t.Fatalf("old configuration accepted=%v err=%v", accepted, err)
	}
	if channel.HealthConsecutiveFailures != 2 || channel.HealthStatus != ChannelHealthDegraded {
		t.Fatal("old configuration observation reset persistent availability health")
	}
}

func TestChannelUsecaseFailedHealthWriteKeepsProbeRetryable(t *testing.T) {
	ch := &Channel{ID: 1}
	repo := &retryableHealthRepo{mockChannelRepo: &mockChannelRepo{channels: map[int64]*Channel{1: ch}}, writeErr: fmt.Errorf("health store unavailable")}
	uc := NewChannelUsecase(repo, nil)
	uc.selector.UpdateChannel(ch)
	uc.selector.channels[1].circuitOpenUntil = time.Now().Add(-time.Second).UnixNano()
	selected, err := uc.selector.Select(context.Background(), "g", []*Channel{ch})
	if err != nil || selected.HealthProbeID == "" {
		t.Fatalf("half-open probe=%v err=%v", selected, err)
	}
	o := provider.Observation{ID: "retryable-probe", SourceKind: "channel", SourceID: 1, ConfigurationRevision: ch.UpstreamRevision(), StartedAt: time.Now(), Outcome: provider.HealthSuccess, ProbeID: selected.HealthProbeID}
	accepted, err := uc.RecordUpstreamObservation(context.Background(), o)
	if err == nil || accepted {
		t.Fatalf("failed persistent write accepted=%v err=%v", accepted, err)
	}
	if state := uc.selector.channels[1]; state.circuitOpenUntil == 0 || state.control.probeID != selected.HealthProbeID {
		t.Fatal("failed persistent write consumed recovery probe")
	}
	accepted, err = uc.RecordUpstreamObservation(context.Background(), o)
	if err != nil || !accepted {
		t.Fatalf("same observation retry accepted=%v err=%v", accepted, err)
	}
	if uc.selector.channels[1].circuitOpenUntil != 0 || ch.HealthStatus != ChannelHealthHealthy {
		t.Fatal("retry did not persist and complete successful recovery")
	}
	accepted, err = uc.RecordUpstreamObservation(context.Background(), o)
	if err != nil || accepted {
		t.Fatalf("completed probe duplicate accepted=%v err=%v", accepted, err)
	}
}

func TestWeightedSelectorIneligibleNilCandidateDoesNotPanic(t *testing.T) {
	s := NewWeightedSelector()
	if _, err := s.Select(context.Background(), "g", []*Channel{nil}); err != ErrChannelNotFound {
		t.Fatalf("nil candidate rejection=%v, want ErrChannelNotFound", err)
	}
}
