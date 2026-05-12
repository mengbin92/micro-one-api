package service

import (
	"context"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/internal/channel/biz"
	"micro-one-api/internal/pkg/errors"
)

// ChannelService is the transport layer entry for channel-service.
type ChannelService struct {
	channelv1.UnimplementedChannelServiceServer
	uc *biz.ChannelUsecase
}

func NewChannelService(uc *biz.ChannelUsecase) *ChannelService {
	return &ChannelService{uc: uc}
}

func (s *ChannelService) SelectChannelModel(ctx context.Context, group, model string, excludeFirstPriority bool) (*biz.Channel, error) {
	return s.uc.SelectChannel(ctx, group, model, excludeFirstPriority)
}

func (s *ChannelService) GetChannelModel(ctx context.Context, channelID int64) (*biz.Channel, error) {
	return s.uc.GetChannel(ctx, channelID)
}

func (s *ChannelService) ListAvailableModelsModel(ctx context.Context, group string) ([]string, error) {
	return s.uc.ListAvailableModels(ctx, group)
}

func (s *ChannelService) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest) (*channelv1.SelectChannelReply, error) {
	channel, err := s.uc.SelectChannel(ctx, req.Group, req.Model, req.ExcludeFirstPriority)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.SelectChannelReply{
		Channel: toChannelInfo(channel),
	}, nil
}

func (s *ChannelService) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest) (*channelv1.GetChannelReply, error) {
	channel, err := s.uc.GetChannel(ctx, req.ChannelId)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.GetChannelReply{
		Channel: toChannelInfo(channel),
	}, nil
}

func (s *ChannelService) ListAvailableModels(ctx context.Context, req *channelv1.ListAvailableModelsRequest) (*channelv1.ListAvailableModelsReply, error) {
	models, err := s.uc.ListAvailableModels(ctx, req.Group)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.ListAvailableModelsReply{
		Models: models,
	}, nil
}

func toChannelInfo(channel *biz.Channel) *commonv1.ChannelInfo {
	if channel == nil {
		return nil
	}
	return &commonv1.ChannelInfo{
		Id:                 channel.ID,
		Type:               channel.Type,
		Name:               channel.Name,
		Status:             channel.Status,
		BaseUrl:            channel.BaseURL,
		Group:              channel.Group,
		Models:             channel.ModelsCSV(),
		Priority:           channel.Priority,
		Key:                channel.Key,
		Weight:             channel.Weight,
		TestTime:           channel.TestTime,
		ResponseTime:       channel.ResponseTime,
		Balance:            channel.Balance,
		BalanceUpdatedTime: channel.BalanceUpdatedTime,
		UsedQuota:          channel.UsedQuota,
		ModelMapping:       channel.ModelMapping,
		SystemPrompt:       channel.SystemPrompt,
		Config: &commonv1.ChannelConfig{
			ApiVersion:        channel.Config.APIVersion,
			Region:            channel.Config.Region,
			LibraryId:         channel.Config.LibraryID,
			Plugin:            channel.Config.Plugin,
			VertexAiProjectId: channel.Config.VertexAIProjectID,
		},
	}
}

func toChannelSummary(channel *biz.Channel) *commonv1.ChannelSummary {
	if channel == nil {
		return nil
	}
	return &commonv1.ChannelSummary{
		Id:                 channel.ID,
		Name:               channel.Name,
		Type:               channel.Type,
		Group:              channel.Group,
		Status:             channel.Status,
		Priority:           channel.Priority,
		CreatedAt:          channel.CreatedTime,
		Models:             channel.ModelsCSV(),
		Weight:             channel.Weight,
		TestTime:           channel.TestTime,
		ResponseTime:       channel.ResponseTime,
		Balance:            channel.Balance,
		BalanceUpdatedTime: channel.BalanceUpdatedTime,
		UsedQuota:          channel.UsedQuota,
	}
}

func (s *ChannelService) ListChannels(ctx context.Context, req *channelv1.ListChannelsRequest) (*channelv1.ListChannelsResponse, error) {
	channels, total, err := s.uc.ListChannels(ctx, req.Page, req.PageSize, req.Keyword, req.Group, req.Status, req.Type)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	result := make([]*commonv1.ChannelSummary, len(channels))
	for i, ch := range channels {
		result[i] = toChannelSummary(ch)
	}
	return &channelv1.ListChannelsResponse{
		Channels: result,
		Total:    total,
	}, nil
}

func (s *ChannelService) CreateChannel(ctx context.Context, req *channelv1.CreateChannelRequest) (*channelv1.CreateChannelResponse, error) {
	cfg := commonv1.ChannelConfig{}
	if req.Config != nil {
		cfg = *req.Config
	}
	channel := &biz.Channel{
		Type:         req.Type,
		Name:         req.Name,
		BaseURL:      req.BaseUrl,
		Key:          req.Key,
		Models:       biz.SplitCSV(req.Models),
		Group:        req.Group,
		Priority:     req.Priority,
		Status:       biz.ChannelStatusEnabled,
		Weight:       req.Weight,
		ModelMapping: req.ModelMapping,
		SystemPrompt: req.SystemPrompt,
		Config: biz.ChannelConfig{
			APIVersion:        cfg.ApiVersion,
			Region:            cfg.Region,
			LibraryID:         cfg.LibraryId,
			Plugin:            cfg.Plugin,
			VertexAIProjectID: cfg.VertexAiProjectId,
		},
	}
	if err := s.uc.CreateChannel(ctx, channel); err != nil {
		return &channelv1.CreateChannelResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.CreateChannelResponse{
		Success:   true,
		Message:   "ok",
		ChannelId: channel.ID,
	}, nil
}

func (s *ChannelService) UpdateChannel(ctx context.Context, req *channelv1.UpdateChannelRequest) (*channelv1.UpdateChannelResponse, error) {
	channel, err := s.uc.GetChannel(ctx, req.ChannelId)
	if err != nil {
		return &channelv1.UpdateChannelResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	if req.Name != "" {
		channel.Name = req.Name
	}
	if req.BaseUrl != "" {
		channel.BaseURL = req.BaseUrl
	}
	if req.Key != "" {
		channel.Key = req.Key
	}
	if req.Models != "" {
		channel.Models = biz.SplitCSV(req.Models)
	}
	if req.Group != "" {
		channel.Group = req.Group
	}
	if req.Priority != 0 {
		channel.Priority = req.Priority
	}
	if req.Weight != 0 {
		channel.Weight = req.Weight
	}
	if req.ModelMapping != "" {
		channel.ModelMapping = req.ModelMapping
	}
	if req.SystemPrompt != "" {
		channel.SystemPrompt = req.SystemPrompt
	}
	if req.Config != nil {
		channel.Config = biz.ChannelConfig{
			APIVersion:        req.Config.ApiVersion,
			Region:            req.Config.Region,
			LibraryID:         req.Config.LibraryId,
			Plugin:            req.Config.Plugin,
			VertexAIProjectID: req.Config.VertexAiProjectId,
		}
	}
	if err := s.uc.UpdateChannel(ctx, channel); err != nil {
		return &channelv1.UpdateChannelResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.UpdateChannelResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *ChannelService) DeleteChannel(ctx context.Context, req *channelv1.DeleteChannelRequest) (*channelv1.DeleteChannelResponse, error) {
	if err := s.uc.DeleteChannel(ctx, req.ChannelId); err != nil {
		return &channelv1.DeleteChannelResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.DeleteChannelResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *ChannelService) ChangeChannelStatus(ctx context.Context, req *channelv1.ChangeChannelStatusRequest) (*channelv1.ChangeChannelStatusResponse, error) {
	if err := s.uc.ChangeChannelStatus(ctx, req.ChannelId, req.Status); err != nil {
		return &channelv1.ChangeChannelStatusResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.ChangeChannelStatusResponse{
		Success: true,
		Message: "ok",
	}, nil
}
