package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	coderws "github.com/coder/websocket"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

var errWSRoutingAdmission = errors.New("websocket routing admission failed")

// Responses carries one active generation on a connection. A subsequent
// response.create must acquire its own immutable reservation before forwarding.
type routingWSTurns struct {
	mu     sync.Mutex
	first  bool
	active *billingv1.ReserveQuotaResponse
	admit  func(context.Context, []byte) (*billingv1.ReserveQuotaResponse, []byte, error)
}

func (t *routingWSTurns) beforeWrite(ctx context.Context, kind coderws.MessageType, payload []byte) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.first {
		t.first = true
		return payload, nil
	}
	if kind != coderws.MessageText {
		return nil, fmt.Errorf("%w: text frames required", errWSRoutingAdmission)
	}
	var frame struct {
		Type string `json:"type"`
	}
	if jsonx.Unmarshal(payload, &frame) != nil {
		return nil, fmt.Errorf("%w: invalid frame", errWSRoutingAdmission)
	}
	if frame.Type != "response.create" {
		return payload, nil
	}
	if t.active != nil {
		return nil, fmt.Errorf("%w: previous response still active", errWSRoutingAdmission)
	}
	reservation, rewritten, err := t.admit(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errWSRoutingAdmission, err)
	}
	t.active = reservation
	return rewritten, nil
}

func (t *routingWSTurns) take() *billingv1.ReserveQuotaResponse {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.active
	t.active = nil
	return r
}

func (s *HTTPServer) newRoutingWSTurns(token, clientModel, resolvedModel string, plan *relaybiz.RelayPlan, channel *relaybiz.Channel, first *billingv1.ReserveQuotaResponse) *routingWSTurns {
	if plan.Auth.RoutingContext == nil {
		return nil
	}
	return &routingWSTurns{active: first, admit: func(ctx context.Context, payload []byte) (*billingv1.ReserveQuotaResponse, []byte, error) {
		requested := extractOpenAIWSClientModel(payload)
		if requested != "" && requested != clientModel && requested != resolvedModel {
			return nil, nil, fmt.Errorf("model changed; open a new websocket")
		}
		p, err := s.getAuthSnapshot(ctx, token)
		if err != nil {
			return nil, nil, err
		}
		auth, err := s.routingAuth(ctx, p)
		if err != nil {
			return nil, nil, err
		}
		if auth.RoutingContext == nil || auth.UserID != plan.Auth.UserID || auth.TokenID != plan.Auth.TokenID || auth.RoutingContext.GroupID != plan.Auth.RoutingContext.GroupID || !authAllowsModel(auth.AllowedModels, clientModel) {
			return nil, nil, fmt.Errorf("routing selection changed; open a new websocket")
		}
		source := routing.Source{Kind: routing.Channel, ID: channel.ID}
		if channel.SubscriptionAccountID > 0 {
			source = routing.Source{Kind: routing.Subscription, ID: channel.SubscriptionAccountID}
		}
		permission, err := s.checkStoredSource(ctx, auth.Group, clientModel, source)
		if err != nil {
			return nil, nil, err
		}
		if !permission.Allowed {
			return nil, nil, fmt.Errorf("routing source no longer authorized")
		}
		rewritten := rewriteOpenAIWSModel(payload, clientModel, resolvedModel)
		reservation, err := s.reserveQuota(ctx, strconv.FormatInt(auth.UserID, 10), generateRequestID(), estimateRawTokens(rewritten), s.BillingModelName(clientModel, resolvedModel, resolvedModel), strconv.FormatInt(channel.ID, 10), routingSubscriptionAccountID(channel), auth.RoutingContext)
		return reservation, rewritten, err
	}}
}
