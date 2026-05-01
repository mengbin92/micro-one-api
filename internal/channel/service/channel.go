package service

import (
	"context"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/internal/channel/biz"
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
		return nil, err
	}
	return &channelv1.SelectChannelReply{
		Channel: toChannelInfo(channel),
	}, nil
}

func (s *ChannelService) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest) (*channelv1.GetChannelReply, error) {
	channel, err := s.uc.GetChannel(ctx, req.ChannelId)
	if err != nil {
		return nil, err
	}
	return &channelv1.GetChannelReply{
		Channel: toChannelInfo(channel),
	}, nil
}

func (s *ChannelService) ListAvailableModels(ctx context.Context, req *channelv1.ListAvailableModelsRequest) (*channelv1.ListAvailableModelsReply, error) {
	models, err := s.uc.ListAvailableModels(ctx, req.Group)
	if err != nil {
		return nil, err
	}
	return &channelv1.ListAvailableModelsReply{
		Models: models,
	}, nil
}

func toChannelInfo(channel *biz.Channel) *channelv1.ChannelInfo {
	if channel == nil {
		return nil
	}
	return &channelv1.ChannelInfo{
		Id:       channel.ID,
		Type:     channel.Type,
		Name:     channel.Name,
		Status:   channel.Status,
		BaseUrl:  channel.BaseURL,
		Group:    channel.Group,
		Models:   channel.ModelsCSV(),
		Priority: channel.Priority,
		Key:      channel.Key,
		Config: &channelv1.ChannelConfig{
			ApiVersion:        channel.Config.APIVersion,
			Region:            channel.Config.Region,
			LibraryId:         channel.Config.LibraryID,
			Plugin:            channel.Config.Plugin,
			VertexAiProjectId: channel.Config.VertexAIProjectID,
		},
	}
}
