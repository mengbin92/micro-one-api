package biz

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"micro-one-api/domain/routing"
)

// RoutingOrderedEnabled gates the Phase F ordered (auto) routing capability.
// Like the other relay routing gates it defaults off.
func RoutingOrderedEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("RELAY_ROUTING_ORDERED")), "true")
}

// RoutingGroupProber is the optional channel capability behind ordered
// routing: a read-only "does this group currently have schedulable upstreams
// for the model" probe that never advances weighted-scheduler state.
type RoutingGroupProber interface {
	HasRoutingCandidates(ctx context.Context, groupID int64, model string) (bool, error)
}

// RoutingSettlementClient is the optional billing capability behind ordered
// routing: per-candidate settlement qualification. A nil client (or an RPC
// error) fails closed — reserve stays the settlement authority.
type RoutingSettlementClient interface {
	CheckRoutingSettlement(ctx context.Context, userID, groupID int64) (allowed bool, reason string, err error)
}

func (uc *RelayUsecase) SetRoutingSettlementClient(c RoutingSettlementClient) {
	uc.routingSettlement = c
}

// resolveOrderedRouting walks the token's explicit ordered candidate list in
// user order. Advancement to the next candidate happens ONLY here — before
// the first upstream send, before reservation. Disabled/archived groups and
// groups without candidate resources advance; missing access, failed
// settlement qualification and an exhausted list terminate (never falling
// back to the global group list).
func (uc *RelayUsecase) resolveOrderedRouting(ctx context.Context, auth *AuthSnapshot, opts RoutingResolveOptions) error {
	if !RoutingOrderedEnabled() {
		return fmt.Errorf("ordered routing requires relay ordered capability")
	}
	reader, ok := uc.channel.(RoutingGroupReader)
	if !ok {
		return fmt.Errorf("channel routing capability unavailable")
	}
	if err := uc.mergeRoutingEntitlements(ctx, auth); err != nil {
		return err
	}
	candidates := routing.OrderedGroupIDs(auth.RoutingFacts)
	if len(candidates) == 0 {
		return fmt.Errorf("ordered candidate list unavailable")
	}
	now := time.Now().Unix()
	groups := make(map[int64]*routing.Group, len(candidates))
	load := func(gid int64) (*routing.Group, error) {
		if g, seen := groups[gid]; seen {
			return g, nil
		}
		g, err := reader.GetRoutingGroup(ctx, gid)
		if err != nil {
			if !kratoserrors.IsNotFound(err) {
				return nil, err
			}
			g = nil
		}
		groups[gid] = g
		return g, nil
	}
	if opts.BoundGroupID > 0 {
		return uc.bindOrderedGroup(auth, candidates, load, opts.BoundGroupID, now)
	}
	// Sticky scan first: session keys carry the group id, so a bound
	// conversation always lands back on its original group — even when an
	// earlier candidate now has resources.
	if uc.stickyEnabled && uc.sessionStore != nil && strings.TrimSpace(opts.SessionHash) != "" {
		for _, gid := range candidates {
			g, err := load(gid)
			if err != nil {
				return err
			}
			if g == nil {
				continue
			}
			key := routing.SessionKey(&routing.ResolvedRoutingContext{UserID: auth.UserID, TokenID: auth.TokenID, GroupID: gid}, opts.SessionHash)
			if uc.sessionStore.LookupSessionChannel(ctx, g.Key, key) <= 0 {
				continue
			}
			// A bound conversation stays on its original group. When that
			// group can no longer serve it (access revoked, disabled), the
			// client must start a new session instead of silently moving the
			// conversation to another upstream.
			return uc.bindOrderedGroup(auth, candidates, load, gid, now)
		}
	}
	for i, gid := range candidates {
		g, err := load(gid)
		if err != nil {
			return err
		}
		if g == nil || g.Status != "enabled" {
			continue // disabled/archived/deleted group: advance
		}
		if len(routing.AccessSources(auth.RoutingFacts, g, now)) == 0 {
			return fmt.Errorf("routing group %d access denied", gid)
		}
		if uc.routingSettlement != nil {
			allowed, _, err := uc.routingSettlement.CheckRoutingSettlement(ctx, auth.UserID, gid)
			if err != nil {
				return fmt.Errorf("routing settlement check unavailable: %w", err)
			}
			if !allowed {
				return fmt.Errorf("routing group %d settlement unavailable", gid)
			}
		}
		if strings.TrimSpace(opts.Model) != "" {
			prober, ok := uc.channel.(RoutingGroupProber)
			if !ok {
				return fmt.Errorf("channel routing probe capability unavailable")
			}
			has, err := prober.HasRoutingCandidates(ctx, gid, opts.Model)
			if err != nil {
				return err
			}
			if !has {
				continue // the group has no candidate resources for the model: advance
			}
		}
		return uc.finishOrderedAttempt(auth, candidates, load, gid, i, now)
	}
	return fmt.Errorf("no ordered routing group has candidate resources")
}

// bindOrderedGroup re-validates the already-bound group for ordered sessions
// (Responses/WS turns, pre-send rechecks). It never advances: when the bound
// group drops out of the candidate list, loses access, or is disabled, the
// binding is stale and the client must start a new request.
func (uc *RelayUsecase) bindOrderedGroup(auth *AuthSnapshot, candidates []int64, load func(int64) (*routing.Group, error), boundGID, now int64) error {
	ordinal := -1
	for i, gid := range candidates {
		if gid == boundGID {
			ordinal = i
			break
		}
	}
	if ordinal < 0 {
		return fmt.Errorf("routing selection changed; start a new request")
	}
	return uc.finishOrderedAttempt(auth, candidates, load, boundGID, ordinal, now)
}

func (uc *RelayUsecase) finishOrderedAttempt(auth *AuthSnapshot, candidates []int64, load func(int64) (*routing.Group, error), gid int64, ordinal int, now int64) error {
	g, err := load(gid)
	if err != nil {
		return err
	}
	resolved, err := routing.ResolveOrdered(auth.UserID, auth.TokenID, auth.RoutingFacts, g, ordinal, now)
	if err != nil {
		return err
	}
	auth.Group = resolved.GroupKey
	auth.RoutingContext = resolved
	return nil
}
