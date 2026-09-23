package service

import (
	"context"
	"fmt"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	relayv1 "micro-one-api/api/relay/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/safecast"
	"micro-one-api/platform/routingdto"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
)

// RelayGrpcService implements the gRPC RelayServiceServer interface.
type RelayGrpcService struct {
	relayv1.UnimplementedRelayServiceServer
	identityClient  identityv1.IdentityServiceClient
	channelClient   channelv1.ChannelServiceClient
	logClient       logv1.LogServiceClient
	billingClient   billingv1.BillingServiceClient
	providerFactory *relayprovider.ProviderFactory
	relayUsecase    *relaybiz.RelayUsecase
}

// NewRelayGrpcService creates a new gRPC relay service.
func NewRelayGrpcService(
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	logClient logv1.LogServiceClient,
	billingClient billingv1.BillingServiceClient,
	providerFactory *relayprovider.ProviderFactory,
	relayUsecase *relaybiz.RelayUsecase,
) *RelayGrpcService {
	return &RelayGrpcService{
		identityClient:  identityClient,
		channelClient:   channelClient,
		logClient:       logClient,
		billingClient:   billingClient,
		providerFactory: providerFactory,
		relayUsecase:    relayUsecase,
	}
}

// ChatCompletion handles synchronous chat completion via gRPC.
func (s *RelayGrpcService) ChatCompletion(ctx context.Context, req *relayv1.ChatCompletionRequest) (*relayv1.ChatCompletionResponse, error) {
	startedAt := time.Now()
	token, err := extractTokenFromMetadata(ctx)
	if err != nil {
		return nil, err
	}

	rootRequestID := "grpc_root_" + uuid.NewString()
	ctx = requesttrace.WithAttempt(ctx, requesttrace.Attempt{RootRequestID: rootRequestID})
	plan, err := s.relayUsecase.Plan(ctx, relaybiz.RelayRequest{
		Token:     token,
		Model:     req.Model,
		RequestID: rootRequestID,
	})
	if err != nil {
		return nil, err
	}

	providerReq := &relayprovider.ChatCompletionsRequest{
		Model: plan.ResolvedModel,
	}
	for _, m := range req.Messages {
		providerReq.Messages = append(providerReq.Messages, relayprovider.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	if req.Temperature != nil {
		providerReq.Temperature = req.Temperature
	}
	if req.MaxTokens != nil {
		maxTokens := int(*req.MaxTokens)
		providerReq.MaxTokens = &maxTokens
	}

	retryExecutor := s.relayUsecase.NewRetryExecutor()
	var resp *relayprovider.ChatCompletionsResponse

	result := retryExecutor.ExecuteWithCandidates(ctx, plan, 0, func(ctx context.Context, ch *relaybiz.Channel) error {
		trace := requesttrace.FromContext(ctx)
		requestID := fmt.Sprintf("grpc_%d", time.Now().UnixNano())
		estimatedTokens := estimateTokensForGRPC(providerReq)
		resolvedModel := relaybiz.ResolveChannelModel(ch, plan.BaseModel())
		providerReq.Model = resolvedModel
		if plan.Auth.RoutingContext != nil {
			capability, err := s.billingClient.GetRoutingCapabilities(ctx, &billingv1.GetRoutingCapabilitiesRequest{})
			if err != nil || capability.GetRequestSnapshotVersion() != 2 || (plan.Auth.RoutingContext.TokenMode == "fixed" && !capability.GetFixedRouting()) || (subscriptionbiz.EntitlementsEnabled() && !capability.GetSubscriptionContracts()) {
				metrics.RoutingAdmissionRejected.WithLabelValues("reserve", "billing_capability").Inc()
				applogger.Log.Warn("routing admission rejected", zap.String("operation", "reserve"), zap.String("reason", "billing_capability"), zap.Int64("user_id", plan.Auth.UserID), zap.Int64("token_id", plan.Auth.TokenID), zap.String("request_id", requestID), zap.Error(err))
				return fmt.Errorf("billing routing capability unavailable")
			}
		}

		reservation, reserveErr := s.billingClient.ReserveQuota(ctx, &billingv1.ReserveQuotaRequest{
			RoutingContext:  routingdto.ContextToProto(plan.Auth.RoutingContext),
			UserId:          fmt.Sprintf("%d", plan.Auth.UserID),
			RequestId:       requestID,
			RootRequestId:   trace.RootRequestID,
			AttemptNumber:   trace.Number,
			SourceKind:      trace.SourceKind,
			UpstreamModelId: resolvedModel,
			EstimatedTokens: estimatedTokens,
			Model:           resolvedModel,
			ChannelId:       fmt.Sprintf("%d", ch.ID),
		})
		if reserveErr != nil {
			return reserveErr
		}
		if reservation == nil || !reservation.Success {
			return fmt.Errorf("quota reservation failed")
		}
		if c := plan.Auth.RoutingContext; c != nil && (reservation.RequestSnapshotVersion != 2 || reservation.RoutingContextHash != c.Digest() || len(reservation.RequestSnapshotHash) != 64) {
			_, _ = s.billingClient.ReleaseQuota(ctx, &billingv1.ReleaseQuotaRequest{ReservationId: reservation.ReservationId, Reason: "routing snapshot mismatch"})
			return fmt.Errorf("billing routing snapshot mismatch")
		}

		provider, provErr := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
			APIVersion: ch.Config.APIVersion,
		})
		if provErr != nil {
			_, _ = s.billingClient.ReleaseQuota(ctx, &billingv1.ReleaseQuotaRequest{
				ReservationId: reservation.ReservationId,
				Reason:        "failed to create provider",
			})
			return provErr
		}

		resp, err = provider.ChatCompletions(ctx, providerReq)
		if err != nil {
			_, _ = s.billingClient.ReleaseQuota(ctx, &billingv1.ReleaseQuotaRequest{
				ReservationId: reservation.ReservationId,
				Reason:        "upstream error",
			})
			return err
		}

		actualTokens := int64(resp.Usage.TotalTokens)
		commitResp, commitErr := s.billingClient.CommitQuota(ctx, &billingv1.CommitQuotaRequest{
			ReservationId:    reservation.ReservationId,
			ActualTokens:     actualTokens,
			Success:          true,
			SourceKind:       trace.SourceKind,
			UpstreamModelId:  resolvedModel,
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
		})
		if commitErr != nil {
			// The upstream response has already completed. Do not let the
			// retry executor replay the provider request and charge twice.
			return relaybiz.MarkPostForwardError(commitErr)
		}
		if commitResp == nil || !commitResp.GetSuccess() {
			message := "empty quota commit response"
			if commitResp != nil {
				message = commitResp.GetErrorMessage()
			}
			return relaybiz.MarkPostForwardError(fmt.Errorf("grpc quota commit failed: %s", message))
		}
		usageResp, usageErr := s.channelClient.RecordChannelUsage(ctx, &channelv1.RecordChannelUsageRequest{
			ChannelId:     ch.ID,
			Quota:         actualTokens,
			ReservationId: reservation.ReservationId,
		})
		if usageErr != nil || usageResp == nil || !usageResp.GetSuccess() {
			applogger.Log.Warn("grpc channel usage record failed", zap.String("reservation_id", reservation.ReservationId), zap.Error(usageErr))
		}
		// Sprint 4: record model usage stats (best-effort).
		modelResp, modelErr := s.channelClient.RecordModelUsage(ctx, &channelv1.RecordModelUsageRequest{
			ModelId:      relaybiz.RelayModelName(req.Model),
			TokenCount:   actualTokens,
			RequestCount: 1,
		})
		if modelErr != nil || modelResp == nil || !modelResp.GetSuccess() {
			applogger.Log.Warn("grpc model usage record failed", zap.String("reservation_id", reservation.ReservationId), zap.Error(modelErr))
		}
		if plan.Auth != nil && plan.Auth.TokenID > 0 && actualTokens > 0 {
			quotaResp, quotaErr := s.identityClient.ConsumeTokenQuota(ctx, &identityv1.ConsumeTokenQuotaRequest{
				UserId: plan.Auth.UserID, TokenId: plan.Auth.TokenID, Amount: actualTokens, ReservationId: reservation.ReservationId,
			})
			if quotaErr != nil || quotaResp == nil || !quotaResp.GetSuccess() {
				applogger.Log.Warn("grpc token quota record failed", zap.String("reservation_id", reservation.ReservationId), zap.Error(quotaErr))
				message := "token quota record failed"
				if quotaErr != nil {
					message = quotaErr.Error()
				} else if quotaResp != nil && quotaResp.GetMessage() != "" {
					message = quotaResp.GetMessage()
				}
				return relaybiz.MarkPostForwardError(fmt.Errorf("%s", message))
			}
		}
		if s.logClient != nil {
			logResp, logErr := s.logClient.IngestLog(ctx, &logv1.IngestLogRequest{Level: "info", Message: "grpc chat completion completed", Source: "relay-grpc", RequestId: requestID, RootRequestId: trace.RootRequestID, AttemptNumber: trace.Number, ReservationId: reservation.ReservationId, SourceKind: trace.SourceKind, UpstreamModelId: resolvedModel, UserId: plan.Auth.UserID, ModelName: resolvedModel, Quota: actualTokens, PromptTokens: int64(resp.Usage.PromptTokens), CompletionTokens: int64(resp.Usage.CompletionTokens), ChannelId: ch.ID, DedupeKey: reservation.ReservationId})
			if logErr != nil || logResp == nil {
				applogger.Log.Warn("grpc usage log failed", zap.String("reservation_id", reservation.ReservationId), zap.Error(logErr))
			}
		}
		return nil
	})
	resultLabel := "success"
	if result.Err != nil {
		resultLabel = "error"
	}
	if plan.SelectionEvent != nil {
		if result.Channel != nil {
			plan.SelectionEvent.UpstreamModelID = relaybiz.ResolveChannelModel(result.Channel, plan.BaseModel())
			plan.SelectionEvent.FinalSourceID = result.Channel.ID
			plan.SelectionEvent.FinalKind = relaybiz.UpstreamSourceChannel
			if result.Channel.SubscriptionAccountID > 0 {
				plan.SelectionEvent.FinalSourceID = result.Channel.SubscriptionAccountID
				plan.SelectionEvent.FinalKind = relaybiz.UpstreamSourceSubscription
			}
		}
		relaybiz.FinalizeSelectionResult(s.relayUsecase.GetSelectionRecorder(), *plan.SelectionEvent, resultLabel, result.FallbackReason, result.Fallback, time.Since(startedAt))
	}

	if result.Err != nil {
		return nil, result.Err
	}

	return convertToGRPCResponse(resp)
}

// ListModels lists available models via gRPC.
func (s *RelayGrpcService) ListModels(ctx context.Context, req *relayv1.ListModelsRequest) (*relayv1.ListModelsResponse, error) {
	token, err := extractTokenFromMetadata(ctx)
	if err != nil {
		return nil, err
	}

	authResp, err := s.identityClient.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{
		Token: token,
	})
	if err != nil {
		return nil, err
	}

	auth := &relaybiz.AuthSnapshot{UserID: authResp.UserId, TokenID: authResp.TokenId, Group: authResp.Group,
		UserEnabled: authResp.UserEnabled, TokenEnabled: authResp.TokenEnabled, RoutingFacts: routingdto.FactsFromProto(authResp.RoutingFacts), RoutingContextVersion: authResp.RoutingContextVersion}
	if err := s.relayUsecase.ResolveRoutingContext(ctx, auth, relaybiz.RoutingResolveOptions{}); err != nil {
		return nil, err
	}
	authResp.Group = auth.Group
	routingGroupID := routing.SelectedGroupID(auth.RoutingFacts)
	if auth.RoutingContext != nil {
		routingGroupID = auth.RoutingContext.GroupID
	}
	modelsReq := &channelv1.ListAvailableModelsRequest{
		Group:          authResp.Group,
		RoutingGroupId: routingGroupID,
	}
	if candidates := routing.OrderedGroupIDs(auth.RoutingFacts); len(candidates) > 0 {
		// Ordered tokens list the union of their candidate groups' models.
		modelsReq.RoutingGroupId = 0
		modelsReq.RoutingGroupIds = candidates
	}
	modelsResp, err := s.channelClient.ListAvailableModels(ctx, modelsReq)
	if err != nil {
		return nil, err
	}

	models := modelsResp.Models
	if len(authResp.AllowedModels) > 0 {
		allowed := make(map[string]bool)
		for _, m := range authResp.AllowedModels {
			allowed[strings.ToLower(relaybiz.RelayModelName(m))] = true
		}
		filtered := make([]string, 0, len(models))
		for _, m := range models {
			if allowed[strings.ToLower(relaybiz.RelayModelName(m))] {
				filtered = append(filtered, m)
			}
		}
		models = filtered
	}

	result := &relayv1.ListModelsResponse{}
	for _, m := range models {
		result.Models = append(result.Models, &relayv1.ModelInfo{
			Id:      m,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "organization",
		})
	}
	return result, nil
}

func extractTokenFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fmt.Errorf("missing metadata")
	}

	vals := md.Get("authorization")
	if len(vals) == 0 {
		vals = md.Get("x-api-key")
		if len(vals) == 0 {
			return "", fmt.Errorf("missing authorization header")
		}
		return vals[0], nil
	}

	token := vals[0]
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	return token, nil
}

func convertToGRPCResponse(resp *relayprovider.ChatCompletionsResponse) (*relayv1.ChatCompletionResponse, error) {
	result := &relayv1.ChatCompletionResponse{
		Id:      resp.ID,
		Object:  resp.Object,
		Created: resp.Created,
		Model:   resp.Model,
	}
	if resp.Usage.TotalTokens > 0 {
		promptTokens, err := safecast.IntToInt32(resp.Usage.PromptTokens)
		if err != nil {
			return nil, err
		}
		completionTokens, err := safecast.IntToInt32(resp.Usage.CompletionTokens)
		if err != nil {
			return nil, err
		}
		totalTokens, err := safecast.IntToInt32(resp.Usage.TotalTokens)
		if err != nil {
			return nil, err
		}
		result.Usage = &relayv1.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		}
	}
	for _, c := range resp.Choices {
		index, err := safecast.IntToInt32(c.Index)
		if err != nil {
			return nil, err
		}
		result.Choices = append(result.Choices, &relayv1.Choice{
			Index: index,
			Message: &relayv1.Message{
				Role:    c.Message.Role,
				Content: c.Message.Content,
			},
			FinishReason: c.FinishReason,
		})
	}
	return result, nil
}

func estimateTokensForGRPC(req *relayprovider.ChatCompletionsRequest) int64 {
	tokens := int64(0)
	for _, msg := range req.Messages {
		tokens += int64(len(msg.Content) / 4)
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		tokens += int64(*req.MaxTokens)
	} else {
		tokens += 1000
	}
	return tokens
}
