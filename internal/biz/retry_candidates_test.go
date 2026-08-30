package biz

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastRetryPolicy returns a retry policy with negligible backoff for tests.
func fastRetryPolicy(maxAttempts int) *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:     maxAttempts,
		InitialInterval: time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      1.0,
		RetryableStatus: map[int]bool{502: true},
	}
}

func TestRoutingCandidateListNextSkipsExcluded(t *testing.T) {
	winner := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 5},
		Priority: 10,
		Weight:   1,
		Channel:  &Channel{ID: 5, SubscriptionAccountID: 5},
		Account:  &SubscriptionAccount{ID: 5},
	}
	alternate := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 7},
		Priority: 10,
		Weight:   1,
		Channel:  &Channel{ID: 7},
	}
	list := newRoutingCandidateList("default", "gpt-4o", "gpt-4o", winner, alternate)

	// Winner is pre-excluded: first Next() returns the alternate.
	next := list.Next()
	require.NotNil(t, next)
	assert.Equal(t, int64(7), next.Channel.ID)

	// Exhausting the list returns nil.
	assert.Nil(t, list.Next())
}

func TestRoutingCandidateListExcludeAdvances(t *testing.T) {
	cands := []RoutingCandidate{
		{Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 1}, Channel: &Channel{ID: 1}},
		{Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 2}, Channel: &Channel{ID: 2}},
		{Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 3}, Channel: &Channel{ID: 3}},
	}
	list := &RoutingCandidateList{Candidates: cands}
	list.Exclude(cands[0].Identity)
	list.Exclude(cands[1].Identity)

	next := list.Next()
	require.NotNil(t, next)
	assert.Equal(t, int64(3), next.Channel.ID)
	assert.Nil(t, list.Next())
}

// recordingFallbackSelector captures the exclusion set passed to the unified
// fallback so tests can assert request-scoped failures are forwarded.
type recordingFallbackSelector struct {
	channel  *Channel
	excluded map[RoutingSourceIdentity]bool
	calls    int
}

func (r *recordingFallbackSelector) SelectFallbackRoutingSource(_ context.Context, _, _, _ string, excluded map[RoutingSourceIdentity]bool) (*Channel, error) {
	r.calls++
	r.excluded = make(map[RoutingSourceIdentity]bool, len(excluded))
	maps.Copy(r.excluded, excluded)
	if r.channel == nil {
		return nil, errors.New("no fallback routing source")
	}
	return r.channel, nil
}

func TestRetryExecutor_ExecuteWithCandidates_WalksPrecomputedList(t *testing.T) {
	selector := &mockChannelSelector{}
	exec := NewRetryExecutor(fastRetryPolicy(3), selector).WithFallbackSelector(&recordingFallbackSelector{})

	// HIGH-1: retry is namespace-locked. Both candidates are subscription
	// accounts so the walk stays in-namespace (a cross-namespace alternate
	// would be skipped — see TestRetryExecutor_ExecuteWithCandidates_NamespaceLock).
	winner := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 5},
		Channel:  &Channel{ID: 5, SubscriptionAccountID: 5},
		Account:  &SubscriptionAccount{ID: 5},
	}
	alternate := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 7},
		Channel:  &Channel{ID: 7, SubscriptionAccountID: 7},
		Account:  &SubscriptionAccount{ID: 7},
	}
	plan := &RelayPlan{
		Auth:       &AuthSnapshot{Group: "default"},
		Channel:    winner.Channel,
		Account:    winner.Account,
		Candidates: newRoutingCandidateList("default", "gpt-4o", "gpt-4o", winner, alternate),
	}

	var attempts []int64
	result := exec.ExecuteWithCandidates(context.Background(), plan, 5, func(_ context.Context, ch *Channel) error {
		attempts = append(attempts, ch.ID)
		if len(attempts) == 1 {
			return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
		}
		return nil
	})

	require.NoError(t, result.Err)
	assert.Equal(t, []int64{5, 7}, attempts, "retry must walk the precomputed list without a fresh selection RPC")
	assert.True(t, result.Fallback, "switching from subscription 5 to subscription 7 is a fallback")
	assert.Equal(t, 0, selector.callIdx, "candidate walk must not hit the legacy selector")
}

func TestRetryExecutor_ExecuteWithCandidates_FallbackSeesExclusionSet(t *testing.T) {
	selector := &mockChannelSelector{}
	fallback := &recordingFallbackSelector{channel: &Channel{ID: 9, SubscriptionAccountID: 9}}
	exec := NewRetryExecutor(fastRetryPolicy(3), selector).WithFallbackSelector(fallback)

	// HIGH-1: namespace-locked. Initial subscription → both candidates are
	// subscription accounts; the channel-namespace fallback entry is not
	// exercised here.
	winner := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 5},
		Channel:  &Channel{ID: 5, SubscriptionAccountID: 5},
	}
	alternate := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 7},
		Channel:  &Channel{ID: 7, SubscriptionAccountID: 7},
	}
	plan := &RelayPlan{
		Auth:       &AuthSnapshot{Group: "default"},
		Channel:    winner.Channel,
		Candidates: newRoutingCandidateList("default", "gpt-4o", "gpt-4o", winner, alternate),
	}

	calls := 0
	result := exec.ExecuteWithCandidates(context.Background(), plan, 0, func(_ context.Context, ch *Channel) error {
		calls++
		if calls <= 2 {
			return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
		}
		return nil
	})

	require.NoError(t, result.Err)
	assert.Equal(t, 3, calls, "winner fails, alternate fails, fallback succeeds")
	require.Equal(t, 1, fallback.calls)
	assert.True(t, fallback.excluded[RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 5}], "fallback must see failed subscription account")
	assert.True(t, fallback.excluded[RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 7}], "fallback must see failed alternate subscription account")
}

func TestRetryExecutor_ExecuteWithCandidates_ExhaustedListSameSourceRetry(t *testing.T) {
	selector := &mockChannelSelector{}
	// No fallback wired and single-candidate list: after the winner fails and
	// the list is exhausted, the executor retries the SAME source (not a
	// fallback) until the retry budget runs out.
	winner := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 1},
		Channel:  &Channel{ID: 1},
	}
	plan := &RelayPlan{
		Auth:       &AuthSnapshot{Group: "default"},
		Channel:    winner.Channel,
		Candidates: newRoutingCandidateList("default", "gpt-4o", "gpt-4o", winner),
	}

	exec := NewRetryExecutor(fastRetryPolicy(2), selector)
	calls := 0
	result := exec.ExecuteWithCandidates(context.Background(), plan, 0, func(_ context.Context, ch *Channel) error {
		calls++
		return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
	})

	assert.Error(t, result.Err)
	assert.Equal(t, 2, calls, "same-source retry consumes the remaining budget")
	assert.False(t, result.Fallback, "retrying the failed source is not a fallback")
}

// TestRetryExecutor_ExecuteWithCandidates_NamespaceLockProhibitsCrossSource
// pins the HIGH-1 fix: when the initial source is an API-key channel, a retry
// MUST NOT switch to a subscription-account projection even if one is present
// in the candidate list. The HTTP transport retry closures build the provider
// directly from ch.Key, which is empty on a projection → upstream 401 that is
// not retryable. The candidate walk must skip the cross-namespace alternate.
func TestRetryExecutor_ExecuteWithCandidates_NamespaceLockProhibitsCrossSource(t *testing.T) {
	selector := &mockChannelSelector{}
	exec := NewRetryExecutor(fastRetryPolicy(3), selector).WithFallbackSelector(&recordingFallbackSelector{})

	// Initial source is an API-key channel (ID 3, no SubscriptionAccountID).
	apiKeyChan := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 3},
		Channel:  &Channel{ID: 3, Key: "sk-apikey"},
	}
	// Alternate is a subscription projection (empty Key by design).
	subProjection := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: 5},
		Channel:  &Channel{ID: 5, SubscriptionAccountID: 5}, // Key intentionally empty
	}
	plan := &RelayPlan{
		Auth:       &AuthSnapshot{Group: "default"},
		Channel:    apiKeyChan.Channel,
		Candidates: newRoutingCandidateList("default", "gpt-4o", "gpt-4o", apiKeyChan, subProjection),
	}

	var seenKeys []string
	result := exec.ExecuteWithCandidates(context.Background(), plan, 0, func(_ context.Context, ch *Channel) error {
		seenKeys = append(seenKeys, ch.Key)
		return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
	})

	// Every channel handed to the closure must carry a real API key: the
	// subscription projection (empty Key) was never selected.
	for _, k := range seenKeys {
		assert.NotEqual(t, "", k, "retry must not hand a keyless subscription projection to the HTTP closure")
	}
	// The projection was skipped; the API-key channel was retried in-place
	// (same-source) because no same-namespace alternate exists.
	assert.False(t, result.Fallback, "no same-namespace alternate → same-source retry, not a fallback")
}

// TestRetryExecutor_ExecuteWithCandidates_APIKeyChannelSameTierFallback is
// the P1.1 regression test for roadmap §9.1 risk 1: when two ordinary
// API-key channels share the same priority tier and the first one fails,
// retry must reach its sibling in the same tier — not skip the whole tier
// (the original excludeFirstPriority bug) and not jump to a lower tier.
// The candidate list is precomputed, so the walk is deterministic.
func TestRetryExecutor_ExecuteWithCandidates_APIKeyChannelSameTierFallback(t *testing.T) {
	selector := &mockChannelSelector{}
	exec := NewRetryExecutor(fastRetryPolicy(3), selector).WithFallbackSelector(&recordingFallbackSelector{})

	// Two ordinary API-key channels at the same priority (no SubscriptionAccountID).
	primary := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 10},
		Priority: 10,
		Channel:  &Channel{ID: 10, Key: "sk-primary", Priority: 10},
	}
	sibling := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 11},
		Priority: 10,
		Channel:  &Channel{ID: 11, Key: "sk-sibling", Priority: 10},
	}
	lower := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 12},
		Priority: 1,
		Channel:  &Channel{ID: 12, Key: "sk-lower", Priority: 1},
	}
	plan := &RelayPlan{
		Auth:       &AuthSnapshot{Group: "default"},
		Channel:    primary.Channel,
		Candidates: newRoutingCandidateList("default", "gpt-4o", "gpt-4o", primary, sibling, lower),
	}

	var seenIDs []int64
	result := exec.ExecuteWithCandidates(context.Background(), plan, 0, func(_ context.Context, ch *Channel) error {
		seenIDs = append(seenIDs, ch.ID)
		if ch.ID == 10 {
			return &RetryableError{Status: 502, Err: errors.New("primary failed")}
		}
		return nil // sibling succeeds
	})

	require.NoError(t, result.Err)
	// The sibling (ID=11) at the same priority tier was reached, not skipped.
	assert.Equal(t, []int64{10, 11}, seenIDs,
		"failed channel must fall back to its same-tier sibling, not skip the tier")
	assert.True(t, result.Fallback, "switching from channel 10 to 11 is a source switch")
}

// TestRetryExecutor_ExecuteWithCandidates_APIKeyChannelExhaustsTierThenLower
// extends the same-tier scenario: when ALL same-priority siblings fail, retry
// must continue into the next lower tier rather than giving up — the core
// improvement over the old excludeFirstPriority approach.
func TestRetryExecutor_ExecuteWithCandidates_APIKeyChannelExhaustsTierThenLower(t *testing.T) {
	selector := &mockChannelSelector{}
	exec := NewRetryExecutor(fastRetryPolicy(5), selector).WithFallbackSelector(&recordingFallbackSelector{})

	primary := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 20},
		Priority: 10,
		Channel:  &Channel{ID: 20, Key: "sk-p", Priority: 10},
	}
	sibling := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 21},
		Priority: 10,
		Channel:  &Channel{ID: 21, Key: "sk-s", Priority: 10},
	}
	lower := RoutingCandidate{
		Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: 22},
		Priority: 1,
		Channel:  &Channel{ID: 22, Key: "sk-l", Priority: 1},
	}
	plan := &RelayPlan{
		Auth:       &AuthSnapshot{Group: "default"},
		Channel:    primary.Channel,
		Candidates: newRoutingCandidateList("default", "gpt-4o", "gpt-4o", primary, sibling, lower),
	}

	var seenIDs []int64
	result := exec.ExecuteWithCandidates(context.Background(), plan, 0, func(_ context.Context, ch *Channel) error {
		seenIDs = append(seenIDs, ch.ID)
		if ch.ID == 20 || ch.ID == 21 {
			return &RetryableError{Status: 502, Err: errors.New("tier-10 failed")}
		}
		return nil // lower tier succeeds
	})

	require.NoError(t, result.Err)
	// Walked: primary(20) → sibling(21) → lower(22). The entire tier-10 was
	// exhausted before dropping to tier-1, proving per-candidate exclusion.
	assert.Equal(t, []int64{20, 21, 22}, seenIDs,
		"must exhaust same-tier siblings before dropping to lower tier")
	assert.True(t, result.Fallback)
}
