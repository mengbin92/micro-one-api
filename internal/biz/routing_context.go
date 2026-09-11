package biz

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

type RoutingEntitlementReader interface {
	GetRoutingEntitlements(context.Context, int64) (*routing.EntitlementFacts, error)
}

func (uc *RelayUsecase) SetRoutingEntitlements(r RoutingEntitlementReader) {
	uc.routingEntitlements = r
}

type RoutingGroupReader interface {
	GetRoutingGroup(context.Context, int64) (*routing.Group, error)
}

func RoutingContextV2Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("RELAY_ROUTING_CONTEXT_V2")), "true")
}

// RoutingResolveOptions controls ordered (auto) routing resolution. The zero
// value preserves the inherit/fixed behavior.
type RoutingResolveOptions struct {
	// SessionHash is the raw conversation hash. The ordered sticky scan keys
	// it per candidate group before probing candidates, so a bound
	// conversation lands back on its original group.
	SessionHash string
	// Model scopes the per-group candidate probe. Empty skips the probe
	// (catalogue listings resolve the first eligible group).
	Model string
	// BoundGroupID pins ordered re-resolution to the original group: bound
	// Responses/WS sessions and pre-send rechecks re-validate that group only
	// and never advance to another candidate.
	BoundGroupID int64
}

func (uc *RelayUsecase) ResolveRoutingContext(ctx context.Context, auth *AuthSnapshot, opts RoutingResolveOptions) error {
	if !RoutingContextV2Enabled() {
		if subscriptionbiz.EntitlementsEnabled() {
			return fmt.Errorf("subscription entitlements require v2 relay")
		}
		if auth != nil && auth.RoutingFacts != nil && (auth.RoutingFacts.TokenMode == "fixed" || auth.RoutingFacts.TokenMode == "ordered") {
			return fmt.Errorf("%s routing requires v2 relay", auth.RoutingFacts.TokenMode)
		}
		return nil
	}
	if auth == nil || auth.RoutingContextVersion != routing.ContextVersion || auth.RoutingFacts == nil || !auth.UserEnabled || !auth.TokenEnabled {
		return fmt.Errorf("identity routing capability unavailable")
	}
	if auth.RoutingFacts.TokenMode == "ordered" {
		return uc.resolveOrderedRouting(ctx, auth, opts)
	}
	reader, ok := uc.channel.(RoutingGroupReader)
	if !ok {
		return fmt.Errorf("channel routing capability unavailable")
	}
	group, err := reader.GetRoutingGroup(ctx, routing.SelectedGroupID(auth.RoutingFacts))
	if err != nil {
		return err
	}
	if err := uc.mergeRoutingEntitlements(ctx, auth); err != nil {
		return err
	}
	resolved, err := routing.Resolve(auth.UserID, auth.TokenID, auth.RoutingFacts, group, time.Now().Unix())
	if err != nil {
		return err
	}
	if resolved.TokenMode == "inherit" && resolved.GroupKey != auth.Group {
		return fmt.Errorf("identity default group projection mismatch")
	}
	auth.Group = resolved.GroupKey
	auth.RoutingContext = resolved
	return nil
}

func (uc *RelayUsecase) mergeRoutingEntitlements(ctx context.Context, auth *AuthSnapshot) error {
	if !subscriptionbiz.EntitlementsEnabled() {
		return nil
	}
	if uc.routingEntitlements == nil {
		return fmt.Errorf("subscription entitlement capability unavailable")
	}
	facts, err := uc.routingEntitlements.GetRoutingEntitlements(ctx, auth.UserID)
	if err != nil {
		return err
	}
	auth.RoutingFacts = routing.WithEntitlements(auth.RoutingFacts, facts)
	return nil
}

// BindRoutingAdmission installs a request-local checker. It never changes the
// accepted pricing context: a group change requires a new logical request.
// Ordered tokens re-validate their bound group only; recheck never advances.
func (uc *RelayUsecase) BindRoutingAdmission(auth *AuthSnapshot, token, clientIP string) {
	if auth == nil || auth.RoutingContext == nil {
		return
	}
	original := *auth.RoutingContext
	auth.recheckRouting = func(ctx context.Context, model string) error {
		fresh, err := uc.identity.GetAuthSnapshot(ctx, token, clientIP)
		if err != nil {
			return err
		}
		if err = uc.ResolveRoutingContext(ctx, fresh, RoutingResolveOptions{BoundGroupID: original.GroupID}); err != nil {
			return err
		}
		if fresh.RoutingContext == nil || fresh.UserID != original.UserID || fresh.TokenID != original.TokenID || fresh.RoutingContext.GroupID != original.GroupID {
			return fmt.Errorf("routing selection changed; start a new request")
		}
		if len(fresh.AllowedModels) > 0 {
			for _, m := range fresh.AllowedModels {
				if strings.EqualFold(RelayModelName(m), RelayModelName(model)) {
					return nil
				}
			}
			return fmt.Errorf("token model permission revoked")
		}
		return nil
	}
}

// RecheckRoutingAdmission is used by transports with their own retry loop.
func RecheckRoutingAdmission(ctx context.Context, auth *AuthSnapshot, model string) error {
	if auth == nil || auth.RoutingContext == nil {
		return nil
	}
	if auth.recheckRouting == nil {
		return fmt.Errorf("routing admission checker unavailable")
	}
	return auth.recheckRouting(ctx, model)
}
