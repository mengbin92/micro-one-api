package biz

import "testing"

func TestUpstreamRouteSelectorWeightedRoundRobin(t *testing.T) {
	selector := NewUpstreamRouteSelector()
	candidates := []UpstreamRouteCandidate{
		{Kind: UpstreamRouteChannel, ID: 1, Priority: 5, Weight: 3},
		{Kind: UpstreamRouteSubscription, ID: 2, Priority: 5, Weight: 1},
	}
	counts := map[UpstreamRouteKind]int{}
	for i := 0; i < 40; i++ {
		counts[selector.Select("default", "glm-5.2", candidates).Kind]++
	}
	if counts[UpstreamRouteChannel] != 30 || counts[UpstreamRouteSubscription] != 10 {
		t.Fatalf("counts = %#v, want channel:30 subscription:10", counts)
	}
}

func TestUpstreamRouteSelectorUsesHighestPriorityTier(t *testing.T) {
	selector := NewUpstreamRouteSelector()
	got := selector.Select("default", "glm-5.2", []UpstreamRouteCandidate{
		{Kind: UpstreamRouteChannel, ID: 1, Priority: 1, Weight: 100},
		{Kind: UpstreamRouteSubscription, ID: 2, Priority: 2, Weight: 1},
	})
	if got.Kind != UpstreamRouteSubscription {
		t.Fatalf("Select() = %#v, want subscription", got)
	}
}
