package biz

import (
	"context"
	"errors"
	"testing"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/provider"
)

type claimingChannelClient struct {
	observingChannelClient
	claims    []provider.ExecutionClaim
	lease     provider.ExecutionLease
	err       error
	blockedID int64
}

func TestExecutionCapacityDenialKeeps429WithoutHealthEvidence(t *testing.T) {
	client := &claimingChannelClient{err: &provider.ExecutionCapacityError{Err: errors.New("capacity exhausted")}}
	uc := NewRelayUsecase(nil, client, nil, nil)
	_, err := uc.AdmitUpstreamExecution(context.Background(), &Channel{ID: 7, UpstreamConfigurationRevision: "v2"})
	if UpstreamStatus(err) != 429 || !IsLocalAdmissionError(err) || UpstreamHealthDisposition(context.Background(), err) != provider.HealthNeutral {
		t.Fatalf("capacity denial = %v, want neutral local 429", err)
	}
}

func (c *claimingChannelClient) ClaimUpstreamExecution(_ context.Context, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	c.claims = append(c.claims, claim)
	if claim.SourceID == c.blockedID {
		return provider.ExecutionLease{}, errors.New("source cooling down")
	}
	return c.lease, c.err
}

func TestRetryExecutorSkipsDeniedCandidateAndForwardsWithFreshProbe(t *testing.T) {
	client := &claimingChannelClient{blockedID: 7, lease: provider.ExecutionLease{ConfigurationRevision: "v2", ProbeID: "probe-8"}}
	uc := NewRelayUsecase(nil, client, nil, &RetryPolicy{MaxAttempts: 2})
	first := &Channel{ID: 7, UpstreamConfigurationRevision: "v2"}
	second := &Channel{ID: 8, UpstreamConfigurationRevision: "v2"}
	plan := &RelayPlan{Auth: &AuthSnapshot{Group: "default"}, Channel: first, GlobalModel: "gpt-5", Candidates: &RoutingCandidateList{Model: "gpt-5", Candidates: []RoutingCandidate{
		{Identity: RoutingSourceIdentityForChannel(first), Channel: first},
		{Identity: RoutingSourceIdentityForChannel(second), Channel: second},
	}}}
	calls := 0
	result := uc.NewRetryExecutor().ExecuteWithCandidates(context.Background(), plan, 0, func(ctx context.Context, ch *Channel) error {
		calls++
		if ch.ID != 8 || ch.HealthProbeID != "probe-8" || requesttrace.FromContext(ctx).HealthProbeID != "probe-8" {
			t.Fatalf("forward used rejected source or stale lease: %+v trace=%+v", ch, requesttrace.FromContext(ctx))
		}
		return nil
	})
	if result.Err != nil || calls != 1 || !result.Fallback || len(client.observations) != 1 || client.observations[0].SourceID != 8 {
		t.Fatalf("result=%+v calls=%d observations=%+v", result, calls, client.observations)
	}
	if second.HealthProbeID != "" {
		t.Fatal("probe lease leaked into shared candidate projection")
	}
}

func TestUpstreamExecutionClaimPreservesSelectedConfigurationAndCopiesLease(t *testing.T) {
	client := &claimingChannelClient{lease: provider.ExecutionLease{ConfigurationRevision: "v2", ProbeID: "fresh-probe"}}
	uc := NewRelayUsecase(nil, client, nil, nil)
	selected := &Channel{ID: -7, SubscriptionAccountID: 7, Key: "selected-key", UpstreamConfigurationRevision: "v2", UpstreamModelID: "gpt-5-prod"}
	admitted, err := uc.AdmitUpstreamExecution(context.Background(), selected)
	if err != nil || admitted == selected || admitted.HealthProbeID != "fresh-probe" || admitted.Key != selected.Key || selected.HealthProbeID != "" {
		t.Fatalf("admission err=%v selected=%+v admitted=%+v", err, selected, admitted)
	}
	if len(client.claims) != 1 || client.claims[0] != (provider.ExecutionClaim{SourceKind: UpstreamSourceSubscription, SourceID: 7, ConfigurationRevision: "v2", UpstreamModelID: "gpt-5-prod"}) {
		t.Fatalf("source identity lost in claim: %+v", client.claims)
	}
}

func TestRetryExecutorRejectsStaleOrUnavailableSourceBeforeForward(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lease provider.ExecutionLease
		err   error
	}{
		{"stale configuration", provider.ExecutionLease{ConfigurationRevision: "v3"}, nil},
		{"capacity cooldown", provider.ExecutionLease{}, errors.New("source cooling down")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &claimingChannelClient{lease: tc.lease, err: tc.err}
			uc := NewRelayUsecase(nil, client, nil, &RetryPolicy{MaxAttempts: 1})
			called := false
			selected := &Channel{ID: 7, Key: "old-key", UpstreamConfigurationRevision: "v2"}
			result := uc.NewRetryExecutor().ExecuteWithInitialChannel(context.Background(), "default", "gpt-5", selected, func(context.Context, *Channel) error { called = true; return nil })
			if result.Err == nil || !IsLocalAdmissionError(result.Err) || called || len(client.observations) != 0 || len(client.healthEvents) != 0 {
				t.Fatalf("stale source result=%v forwarded=%v observations=%+v", result.Err, called, client.observations)
			}
			if selected.UpstreamConfigurationRevision != "v2" || selected.Key != "old-key" || selected.UpstreamModelID != "" {
				t.Fatalf("shared selected source mutated: %+v", selected)
			}
		})
	}
}
