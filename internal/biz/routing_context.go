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

func (uc *RelayUsecase) ResolveRoutingContext(ctx context.Context, auth *AuthSnapshot) error {
	if !RoutingContextV2Enabled() {
		if subscriptionbiz.EntitlementsEnabled() {
			return fmt.Errorf("subscription entitlements require v2 relay")
		}
		if auth != nil && auth.RoutingFacts != nil && auth.RoutingFacts.TokenMode == "fixed" {
			return fmt.Errorf("fixed routing requires v2 relay")
		}
		return nil
	}
	if auth == nil || auth.RoutingContextVersion != routing.ContextVersion || auth.RoutingFacts == nil || !auth.UserEnabled || !auth.TokenEnabled {
		return fmt.Errorf("identity routing capability unavailable")
	}
	reader, ok := uc.channel.(RoutingGroupReader)
	if !ok {
		return fmt.Errorf("channel routing capability unavailable")
	}
	group, err := reader.GetRoutingGroup(ctx, routing.SelectedGroupID(auth.RoutingFacts))
	if err != nil {
		return err
	}
	if subscriptionbiz.EntitlementsEnabled() {
		if uc.routingEntitlements == nil {
			return fmt.Errorf("subscription entitlement capability unavailable")
		}
		facts, err := uc.routingEntitlements.GetRoutingEntitlements(ctx, auth.UserID)
		if err != nil {
			return err
		}
		auth.RoutingFacts = routing.WithEntitlements(auth.RoutingFacts, facts)
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

// BindRoutingAdmission installs a request-local checker. It never changes the
// accepted pricing context: a group change requires a new logical request.
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
		if err = uc.ResolveRoutingContext(ctx, fresh); err != nil {
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
