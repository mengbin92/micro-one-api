package service

import (
	"context"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/internal/channel/biz"
)

type channelServiceRepo struct {
	channel *biz.Channel
	created *biz.Channel
	updated *biz.Channel
}

func (r *channelServiceRepo) FindByID(ctx context.Context, channelID int64) (*biz.Channel, error) {
	return r.channel, nil
}

func (r *channelServiceRepo) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]biz.Ability, error) {
	return []biz.Ability{{Group: group, Model: model, ChannelID: r.channel.ID, Enabled: true, Priority: r.channel.Priority}}, nil
}

func (r *channelServiceRepo) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	return []string{"gpt-4o"}, nil
}

func (r *channelServiceRepo) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
	return []*biz.Channel{r.channel}, 1, nil
}

func (r *channelServiceRepo) CreateChannel(ctx context.Context, channel *biz.Channel) error {
	r.created = channel
	channel.ID = 101
	return nil
}

func (r *channelServiceRepo) UpdateChannel(ctx context.Context, channel *biz.Channel) error {
	r.updated = channel
	return nil
}

func (r *channelServiceRepo) DeleteChannel(ctx context.Context, channelID int64) error { return nil }
func (r *channelServiceRepo) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	return nil
}

func TestChannelServiceOneAPIFields(t *testing.T) {
	repo := &channelServiceRepo{
		channel: &biz.Channel{
			ID:                 1,
			Type:               1,
			Name:               "openai",
			Status:             biz.ChannelStatusEnabled,
			BaseURL:            "https://api.example.com/v1",
			Group:              "default",
			Models:             []string{"gpt-4o"},
			Priority:           9,
			Weight:             3,
			CreatedTime:        1710000000,
			TestTime:           1710000100,
			ResponseTime:       245,
			Balance:            12.5,
			BalanceUpdatedTime: 1710000200,
			UsedQuota:          900,
			ModelMapping:       `{"gpt-4o":"gpt-4o-mini"}`,
			SystemPrompt:       "be concise",
		},
	}
	svc := NewChannelService(biz.NewChannelUsecase(repo, nil))

	getResp, err := svc.GetChannel(context.Background(), &channelv1.GetChannelRequest{ChannelId: 1})
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if getResp.Channel.Weight != 3 || getResp.Channel.ModelMapping != `{"gpt-4o":"gpt-4o-mini"}` || getResp.Channel.SystemPrompt != "be concise" {
		t.Fatalf("GetChannel() one-api fields mismatch: %+v", getResp.Channel)
	}

	listResp, err := svc.ListChannels(context.Background(), &channelv1.ListChannelsRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if listResp.Channels[0].CreatedAt != 1710000000 || listResp.Channels[0].Weight != 3 || listResp.Channels[0].UsedQuota != 900 {
		t.Fatalf("ListChannels() one-api fields mismatch: %+v", listResp.Channels[0])
	}

	createResp, err := svc.CreateChannel(context.Background(), &channelv1.CreateChannelRequest{
		Name:         "new",
		Type:         1,
		BaseUrl:      "https://api.example.com/v1",
		Key:          "sk-test",
		Models:       "gpt-4o",
		Group:        "default",
		Priority:     1,
		Weight:       5,
		ModelMapping: `{"gpt-4o":"gpt-4o-mini"}`,
		SystemPrompt: "reply briefly",
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if !createResp.Success || repo.created.Weight != 5 || repo.created.ModelMapping != `{"gpt-4o":"gpt-4o-mini"}` || repo.created.SystemPrompt != "reply briefly" {
		t.Fatalf("CreateChannel() one-api fields mismatch: resp=%+v created=%+v", createResp, repo.created)
	}

	updateResp, err := svc.UpdateChannel(context.Background(), &channelv1.UpdateChannelRequest{
		ChannelId:          1,
		Weight:             7,
		ModelMapping:       `{"gpt-4o":"gpt-4o"}`,
		SystemPrompt:       "updated",
		Balance:            42.5,
		BalanceUpdatedTime: 1710000300,
	})
	if err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if !updateResp.Success || repo.updated.Weight != 7 || repo.updated.ModelMapping != `{"gpt-4o":"gpt-4o"}` || repo.updated.SystemPrompt != "updated" || repo.updated.Balance != 42.5 || repo.updated.BalanceUpdatedTime != 1710000300 {
		t.Fatalf("UpdateChannel() one-api fields mismatch: resp=%+v updated=%+v", updateResp, repo.updated)
	}
}
