package biz

import (
	"context"
	"fmt"
	"strings"

	"micro-one-api/domain/routing"
)

// RoutingAuthorizationClient is implemented by both production channel
// adapters. A missing authorizer or an RPC failure must never approve reuse.
type RoutingAuthorizationClient interface {
	CanRoute(context.Context, string, string, routing.Source) (routing.Permission, error)
}

// CanRoute asks the same authority used by normal selection. Try the client
// model before its global alias, matching selectAPIKeyChannel and subscription
// selection. Empty models cannot prove authorization for a stored source.
func (uc *RelayUsecase) CanRoute(ctx context.Context, group, clientModel, resolvedModel string, source routing.Source) (routing.Permission, error) {
	if uc == nil {
		return routing.Permission{}, fmt.Errorf("routing authority unavailable")
	}
	authorizer, ok := uc.channel.(RoutingAuthorizationClient)
	if !ok {
		return routing.Permission{}, fmt.Errorf("routing authority unavailable")
	}
	clientModel = RelayModelName(clientModel)
	resolvedModel = RelayModelName(resolvedModel)
	if strings.TrimSpace(clientModel) == "" {
		clientModel = resolvedModel
	}
	if clientModel == "" {
		return routing.Permission{}, nil
	}
	permission, err := authorizer.CanRoute(ctx, group, clientModel, source)
	if err != nil || permission.Allowed {
		return permission, err
	}
	if resolvedModel != "" && resolvedModel != clientModel {
		return authorizer.CanRoute(ctx, group, resolvedModel, source)
	}
	return permission, nil
}

func (uc *RelayUsecase) authorizeRetry(ctx context.Context, group, clientModel, resolvedModel string, channel *Channel) error {
	if channel == nil {
		return fmt.Errorf("retry routing source unavailable")
	}
	source := routing.Source{Kind: routing.Channel, ID: channel.ID}
	if channel.SubscriptionAccountID > 0 {
		source = routing.Source{Kind: routing.Subscription, ID: channel.SubscriptionAccountID}
	}
	permission, err := uc.CanRoute(ctx, group, clientModel, resolvedModel, source)
	if err != nil {
		return err
	}
	if !permission.Allowed {
		return fmt.Errorf("retry routing permission revoked")
	}
	channel.UpstreamModelID = permission.UpstreamModelID
	return nil
}
