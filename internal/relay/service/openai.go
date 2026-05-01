package service

import (
	"context"

	"micro-one-api/internal/relay/biz"
)

// OpenAIService handles the external OpenAI-compatible HTTP surface.
type OpenAIService struct {
	uc *biz.RelayUsecase
}

func NewOpenAIService(uc *biz.RelayUsecase) *OpenAIService {
	return &OpenAIService{uc: uc}
}

func (s *OpenAIService) Plan(ctx context.Context, token, model string) (*biz.RelayPlan, error) {
	return s.uc.Plan(ctx, biz.RelayRequest{
		Token: token,
		Model: model,
	})
}
