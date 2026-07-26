package biz

import (
	"fmt"
	"sync"
)

type UpstreamRouteKind uint8

const (
	UpstreamRouteChannel UpstreamRouteKind = iota + 1
	UpstreamRouteSubscription
)

type UpstreamRouteCandidate struct {
	Kind     UpstreamRouteKind
	ID       int64
	Priority int64
	Weight   int64
}

// UpstreamRouteSelector applies priority tiers across API-key channels and
// subscription accounts, then smooth weighted round-robin within the winning
// tier. The per-source selectors still choose the best concrete channel or
// account; this selector removes the old hard-coded source-type precedence.
type UpstreamRouteSelector struct {
	mu      sync.Mutex
	current map[string]map[string]int64
}

func NewUpstreamRouteSelector() *UpstreamRouteSelector {
	return &UpstreamRouteSelector{current: make(map[string]map[string]int64)}
}

func (s *UpstreamRouteSelector) Select(group, model string, candidates []UpstreamRouteCandidate) UpstreamRouteCandidate {
	if len(candidates) == 0 {
		return UpstreamRouteCandidate{}
	}
	maxPriority := candidates[0].Priority
	for _, candidate := range candidates[1:] {
		if candidate.Priority > maxPriority {
			maxPriority = candidate.Priority
		}
	}
	tier := make([]UpstreamRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Priority == maxPriority {
			if candidate.Weight <= 0 {
				candidate.Weight = 1
			}
			tier = append(tier, candidate)
		}
	}
	if len(tier) == 1 || s == nil {
		return tier[0]
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.current) >= 4096 {
		s.current = make(map[string]map[string]int64)
	}
	scope := group + "\x00" + model
	weights := s.current[scope]
	if weights == nil {
		weights = make(map[string]int64, len(tier))
		s.current[scope] = weights
	}

	best := tier[0]
	bestKey := routeCandidateKey(best)
	var total int64
	for i, candidate := range tier {
		key := routeCandidateKey(candidate)
		weights[key] += candidate.Weight
		total += candidate.Weight
		if i == 0 || weights[key] > weights[bestKey] {
			best = candidate
			bestKey = key
		}
	}
	weights[bestKey] -= total
	return best
}

func routeCandidateKey(candidate UpstreamRouteCandidate) string {
	return fmt.Sprintf("%d:%d", candidate.Kind, candidate.ID)
}
