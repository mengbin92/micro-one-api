package biz

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"micro-one-api/domain/routing"
)

type RoutingGroupReader interface {
	GetRoutingGroup(context.Context, int64) (*routing.Group, error)
}

func RoutingContextV2Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("RELAY_ROUTING_CONTEXT_V2")), "true")
}

func (uc *RelayUsecase) ResolveRoutingContext(ctx context.Context, auth *AuthSnapshot) error {
	if !RoutingContextV2Enabled() {
		return nil
	}
	if auth == nil || auth.RoutingContextVersion != routing.ContextVersion || auth.RoutingFacts == nil || !auth.UserEnabled || !auth.TokenEnabled {
		return fmt.Errorf("identity routing capability unavailable")
	}
	reader, ok := uc.channel.(RoutingGroupReader)
	if !ok {
		return fmt.Errorf("channel routing capability unavailable")
	}
	group, err := reader.GetRoutingGroup(ctx, auth.RoutingFacts.DefaultGroupID)
	if err != nil {
		return err
	}
	resolved, err := routing.ResolveInherited(auth.UserID, auth.TokenID, auth.RoutingFacts, group, time.Now().Unix())
	if err != nil {
		return err
	}
	if resolved.GroupKey != auth.Group {
		return fmt.Errorf("identity default group projection mismatch")
	}
	auth.RoutingContext = resolved
	return nil
}
