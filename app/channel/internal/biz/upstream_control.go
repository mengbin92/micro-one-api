package biz

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/provider"
)

var ErrUpstreamRateLimit = errors.New(429, channelv1.UpstreamControlErrorReason_UPSTREAM_RATE_LIMIT.String(), "upstream capacity temporarily exhausted")

func (c *Channel) UpstreamRevision() string {
	return provider.ConfigurationRevision(fmt.Sprint(c.Type), c.BaseURL, c.Key, c.Config.APIVersion, c.ModelMapping)
}

func (a *SubscriptionAccount) UpstreamRevision() string {
	return provider.ConfigurationRevision(a.Platform, a.AccountType, a.BaseURL, fmt.Sprint(a.CredentialRevision), a.ModelMapping)
}

// Runtime controls belong to the single channel owner, separately from health counters.
// shortcut: process-local fencing and quarantine; share this state before adding channel replicas.
type upstreamControl struct {
	revision             string
	configurationBlocked bool
	capacityUntil        int64
	models               map[string]int64
	probeID              string
	incidentAt           int64
	recoverySuccesses    int
	seen                 map[string]bool
	ids                  [1024]string
	nextID               int
}

func (c *upstreamControl) refresh(revision string) bool {
	if c.revision == revision {
		return false
	}
	known := c.revision != ""
	*c = upstreamControl{revision: revision}
	return known
}

func (c *upstreamControl) recoveryFactor() int64 {
	if c.recoverySuccesses == 0 || c.recoverySuccesses >= 5 {
		return 100
	}
	return int64(c.recoverySuccesses * 20)
}

func (c *upstreamControl) blocked(model string, now int64) bool {
	return c.configurationBlocked || c.models[model] > now
}

func (c *upstreamControl) accept(o provider.Observation, halfOpen bool, now int64) bool {
	if o.ConfigurationRevision != c.revision || o.StartedAt.UnixNano() < c.incidentAt || o.FailureKind == "probe" {
		return false
	}
	if halfOpen && (c.probeID == "" || o.ProbeID != c.probeID) {
		return false
	}
	if !halfOpen && o.ProbeID != "" {
		return false
	}
	if c.seen[o.ID] {
		return false
	}
	if c.seen == nil {
		c.seen = make(map[string]bool)
	}
	delete(c.seen, c.ids[c.nextID])
	c.ids[c.nextID] = o.ID
	c.nextID = (c.nextID + 1) % len(c.ids)
	c.seen[o.ID] = true
	switch o.FailureKind {
	case "capacity":
		delay := o.RetryAfter
		if delay <= 0 {
			delay = 5 * time.Second
		}
		c.capacityUntil = max(c.capacityUntil, now+min(delay, 5*time.Minute).Nanoseconds())
	case "configuration":
		c.configurationBlocked = true
	case "model":
		if c.models == nil {
			c.models = make(map[string]int64)
		}
		for model, until := range c.models {
			if until <= now {
				delete(c.models, model)
			}
		}
		if len(c.models) < 512 {
			c.models[o.UpstreamModelID] = now + (30 * time.Second).Nanoseconds()
		}
	}
	return true
}

func (s *WeightedSelector) RecordObservation(o provider.Observation) bool {
	accepted, _ := s.recordObservation(o, nil)
	return accepted
}

func (s *WeightedSelector) recordObservation(o provider.Observation, persist func() error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.channels[o.SourceID]
	if state == nil {
		return false, nil
	}
	now := time.Now().UnixNano()
	if state.breakerState(now) == circuitOpen && o.Outcome != provider.HealthNeutral {
		return false, nil
	}
	halfOpen := state.breakerState(now) == circuitHalfOpen
	if halfOpen && state.circuitProbeUntil <= now {
		return false, nil
	}
	if !state.control.accept(o, state.breakerState(now) == circuitHalfOpen, now) {
		return false, nil
	}
	if o.Outcome == provider.HealthNeutral {
		if o.ProbeID != "" {
			state.circuitProbeUntil = now
			state.control.probeID = ""
		}
		return true, nil
	}
	// Persist before consuming the probe; a failed write must remain retryable.
	if persist != nil {
		if err := persist(); err != nil {
			delete(state.control.seen, o.ID)
			state.control.ids[(state.control.nextID+len(state.control.ids)-1)%len(state.control.ids)] = ""
			return false, err
		}
	}
	state.control.probeID = ""
	state.recordHealth(o.Outcome == provider.HealthSuccess, o.ResponseTime)
	if !halfOpen && o.Outcome == provider.HealthSuccess && state.control.recoverySuccesses > 0 {
		state.control.recoverySuccesses = min(5, state.control.recoverySuccesses+1)
	}
	return true, nil
}

func (s *SubscriptionAccountSelector) RecordObservation(o provider.Observation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.accounts[o.SourceID]
	if state == nil {
		return false
	}
	now := time.Now().UnixNano()
	if state.breakerState(now) == circuitOpen && o.Outcome != provider.HealthNeutral {
		return false
	}
	halfOpen := state.breakerState(now) == circuitHalfOpen
	if halfOpen && state.circuitProbeUntil <= now {
		return false
	}
	if !state.control.accept(o, state.breakerState(now) == circuitHalfOpen, now) {
		return false
	}
	if o.Outcome == provider.HealthNeutral {
		if o.ProbeID != "" {
			state.circuitProbeUntil = now
			state.control.probeID = ""
		}
		return true
	}
	state.control.probeID = ""
	state.recordHealth(o.Outcome == provider.HealthSuccess)
	if !halfOpen && o.Outcome == provider.HealthSuccess && state.control.recoverySuccesses > 0 {
		state.control.recoverySuccesses = min(5, state.control.recoverySuccesses+1)
	}
	return true
}

func (uc *ChannelUsecase) RecordUpstreamObservation(ctx context.Context, o provider.Observation) (bool, error) {
	uc.runtimeControlMu.Lock()
	defer uc.runtimeControlMu.Unlock()
	switch o.SourceKind {
	case "channel":
		channel, err := uc.repo.FindByID(ctx, o.SourceID)
		if err != nil {
			return false, err
		}
		if o.ConfigurationRevision != channel.UpstreamRevision() {
			return false, nil
		}
		uc.selector.UpdateChannel(channel)
		if o.Outcome == provider.HealthNeutral {
			return uc.selector.RecordObservation(o), nil
		}
		previous := *channel
		var stored *Channel
		accepted, err := uc.selector.recordObservation(o, func() error {
			var persistErr error
			stored, persistErr = uc.repo.RecordHealth(ctx, ChannelHealthEvent{
				ChannelID: o.SourceID, Success: o.Outcome == provider.HealthSuccess,
				Error: o.Error, ResponseTime: o.ResponseTime, CheckedAt: time.Now(),
				ConfigurationRevision: o.ConfigurationRevision,
			}, uc.healthFailureThreshold, uc.healthCooldown)
			return persistErr
		})
		if err != nil {
			return false, err
		}
		if accepted {
			uc.notifyUnavailable(ctx, &previous, stored, ChannelHealthEvent{ChannelID: o.SourceID, Error: o.Error, CheckedAt: time.Now()})
		}
		return accepted, nil
	case "subscription":
		account, err := uc.repo.FindSubscriptionAccountByID(ctx, o.SourceID)
		if err != nil {
			return false, err
		}
		if o.ConfigurationRevision != account.UpstreamRevision() {
			return false, nil
		}
		uc.accountSelector.mu.Lock()
		uc.accountSelector.updateAccountLocked(account)
		uc.accountSelector.mu.Unlock()
		return uc.accountSelector.RecordObservation(o), nil
	default:
		return false, fmt.Errorf("invalid upstream source kind")
	}
}
