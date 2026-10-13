package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

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
	mu        sync.Mutex
	first     bool
	active    *billingv1.ReserveQuotaResponse
	execution *relaybiz.Channel
	admit     func(context.Context, []byte) (*billingv1.ReserveQuotaResponse, []byte, error)
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
	r, _ := t.takeExecution()
	return r
}

func (t *routingWSTurns) takeExecution() (*billingv1.ReserveQuotaResponse, *relaybiz.Channel) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.active
	t.active = nil
	ch := t.execution
	t.execution = nil
	return r, ch
}

func (s *HTTPServer) newRoutingWSTurns(token, clientModel, resolvedModel string, plan *relaybiz.RelayPlan, channel *relaybiz.Channel, first *billingv1.ReserveQuotaResponse) *routingWSTurns {
	initialChannel := *channel
	turns := &routingWSTurns{active: first, execution: &initialChannel}
	admitExecution := func(ctx context.Context) error {
		candidate := *channel
		candidate.HealthProbeID = ""
		admitted, err := s.relayUsecase.AdmitUpstreamExecution(ctx, &candidate)
		if err == nil && admitted.HealthProbeID != "" && admitted.HealthProbeID != channel.HealthProbeID {
			err = fmt.Errorf("upstream recovery requires a fresh websocket connection")
			s.relayUsecase.RecordUpstreamResult(ctx, admitted, err, 0, time.Now())
		}
		if err == nil {
			turns.execution = admitted
		}
		return err
	}
	if plan.Auth.RoutingContext == nil {
		turns.admit = func(ctx context.Context, payload []byte) (*billingv1.ReserveQuotaResponse, []byte, error) {
			requested := extractOpenAIWSClientModel(payload)
			if requested != "" && requested != clientModel && requested != resolvedModel {
				return nil, nil, fmt.Errorf("model changed; open a new websocket")
			}
			fresh, err := s.getRawAuthSnapshot(ctx, token)
			if err != nil {
				return nil, nil, err
			}
			if !fresh.UserEnabled || !fresh.TokenEnabled || fresh.UserId != plan.Auth.UserID || fresh.TokenId != plan.Auth.TokenID || fresh.Group != plan.Auth.Group || !authAllowsModel(fresh.AllowedModels, clientModel) {
				return nil, nil, fmt.Errorf("authorization changed; open a new websocket")
			}
			if channel.UpstreamConfigurationRevision != "" {
				source := routing.Source{Kind: routing.Channel, ID: channel.ID}
				if channel.SubscriptionAccountID > 0 {
					source = routing.Source{Kind: routing.Subscription, ID: channel.SubscriptionAccountID}
				}
				permission, err := s.checkStoredSource(ctx, fresh.Group, clientModel, source)
				if err != nil || !permission.Allowed {
					return nil, nil, fmt.Errorf("routing source no longer authorized")
				}
			}
			if err := admitExecution(ctx); err != nil {
				return nil, nil, err
			}
			rewritten := rewriteOpenAIWSModel(payload, clientModel, resolvedModel)
			requestID := generateRequestID()
			ctx = channelAttemptContext(ctx, requestID, 1, channel, resolvedModel)
			reservation, err := s.reserveQuota(ctx, strconv.FormatInt(fresh.UserId, 10), requestID, estimateRawTokens(rewritten), s.BillingModelName(clientModel, resolvedModel, resolvedModel), strconv.FormatInt(channel.ID, 10), routingSubscriptionAccountID(channel))
			return reservation, rewritten, err
		}
		return turns
	}
	turns.admit = func(ctx context.Context, payload []byte) (*billingv1.ReserveQuotaResponse, []byte, error) {
		requested := extractOpenAIWSClientModel(payload)
		if requested != "" && requested != clientModel && requested != resolvedModel {
			return nil, nil, fmt.Errorf("model changed; open a new websocket")
		}
		p, err := s.getRawAuthSnapshot(ctx, token)
		if err != nil {
			return nil, nil, err
		}
		auth, err := s.routingAuth(ctx, p, plan.Auth.RoutingContext.GroupID)
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
		if err := admitExecution(ctx); err != nil {
			return nil, nil, err
		}
		rewritten := rewriteOpenAIWSModel(payload, clientModel, resolvedModel)
		requestID := generateRequestID()
		ctx = channelAttemptContext(ctx, requestID, 1, channel, resolvedModel)
		reservation, err := s.reserveQuota(ctx, strconv.FormatInt(auth.UserID, 10), requestID, estimateRawTokens(rewritten), s.BillingModelName(clientModel, resolvedModel, resolvedModel), strconv.FormatInt(channel.ID, 10), routingSubscriptionAccountID(channel), auth.RoutingContext)
		return reservation, rewritten, err
	}
	return turns
}
