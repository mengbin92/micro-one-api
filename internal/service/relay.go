package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/metadata"

	relayv1 "micro-one-api/api/relay/v1"
	"micro-one-api/pkg/safecast"
	relaybiz "micro-one-api/internal/biz"
	relayprovider "micro-one-api/domain/upstream/provider"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

// RelayGrpcService implements the gRPC RelayServiceServer interface.
type RelayGrpcService struct {
	relayv1.UnimplementedRelayServiceServer
	identityClient  identityv1.IdentityServiceClient
	channelClient   channelv1.ChannelServiceClient
	billingClient   billingv1.BillingServiceClient
	providerFactory *relayprovider.ProviderFactory
	relayUsecase    *relaybiz.RelayUsecase
}

// NewRelayGrpcService creates a new gRPC relay service.
func NewRelayGrpcService(
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	billingClient billingv1.BillingServiceClient,
	providerFactory *relayprovider.ProviderFactory,
	relayUsecase *relaybiz.RelayUsecase,
) *RelayGrpcService {
	return &RelayGrpcService{
		identityClient:  identityClient,
		channelClient:   channelClient,
		billingClient:   billingClient,
		providerFactory: providerFactory,
		relayUsecase:    relayUsecase,
	}
}

// ChatCompletion handles synchronous chat completion via gRPC.
func (s *RelayGrpcService) ChatCompletion(ctx context.Context, req *relayv1.ChatCompletionRequest) (*relayv1.ChatCompletionResponse, error) {
	token, err := extractTokenFromMetadata(ctx)
	if err != nil {
		return nil, err
	}

	plan, err := s.relayUsecase.Plan(ctx, relaybiz.RelayRequest{
		Token: token,
		Model: req.Model,
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

	result := retryExecutor.Execute(ctx, plan.Auth.Group, req.Model, func(ctx context.Context, ch *relaybiz.Channel) error {
		requestID := fmt.Sprintf("grpc_%d", time.Now().UnixNano())
		estimatedTokens := estimateTokensForGRPC(providerReq)

		reservation, reserveErr := s.billingClient.ReserveQuota(ctx, &billingv1.ReserveQuotaRequest{
			UserId:          fmt.Sprintf("%d", plan.Auth.UserID),
			RequestId:       requestID,
			EstimatedTokens: estimatedTokens,
			Model:           plan.ResolvedModel,
			ChannelId:       fmt.Sprintf("%d", ch.ID),
		})
		if reserveErr != nil {
			return reserveErr
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
		_, _ = s.billingClient.CommitQuota(ctx, &billingv1.CommitQuotaRequest{
			ReservationId: reservation.ReservationId,
			ActualTokens:  actualTokens,
			Success:       true,
		})
		_, _ = s.channelClient.RecordChannelUsage(ctx, &channelv1.RecordChannelUsageRequest{
			ChannelId: ch.ID,
			Quota:     actualTokens,
		})
		return nil
	})

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

	modelsResp, err := s.channelClient.ListAvailableModels(ctx, &channelv1.ListAvailableModelsRequest{
		Group: authResp.Group,
	})
	if err != nil {
		return nil, err
	}

	models := modelsResp.Models
	if len(authResp.AllowedModels) > 0 {
		allowed := make(map[string]bool)
		for _, m := range authResp.AllowedModels {
			allowed[m] = true
		}
		filtered := make([]string, 0, len(models))
		for _, m := range models {
			if allowed[m] {
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
