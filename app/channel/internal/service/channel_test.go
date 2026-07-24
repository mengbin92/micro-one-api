package service

import (
	"context"
	"testing"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
)

type channelServiceRepo struct {
	channel           *biz.Channel
	account           *biz.SubscriptionAccount
	created           *biz.Channel
	updated           *biz.Channel
	createdAccount    *biz.SubscriptionAccount
	updatedAccount    *biz.SubscriptionAccount
	recordedChannelID int64
	recordedQuota     int64
	healthEvent       *biz.ChannelHealthEvent
}

func (r *channelServiceRepo) FindByID(ctx context.Context, channelID int64) (*biz.Channel, error) {
	return r.channel, nil
}

func (r *channelServiceRepo) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]biz.Ability, error) {
	return []biz.Ability{{Group: group, Model: model, ChannelID: r.channel.ID, Enabled: true, Priority: r.channel.Priority}}, nil
}

func (r *channelServiceRepo) ListUnrestrictedChannelsByGroup(ctx context.Context, group string) ([]*biz.Channel, error) {
	return nil, nil
}

func (r *channelServiceRepo) FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error) {
	if r.account != nil && r.account.ID == accountID {
		return r.account, nil
	}
	return nil, biz.ErrSubscriptionAccountNotFound
}

func (r *channelServiceRepo) ListSubscriptionAccountAbilities(ctx context.Context, group, model, platform string) ([]biz.SubscriptionAccountAbility, error) {
	if r.account == nil {
		return nil, nil
	}
	return []biz.SubscriptionAccountAbility{{Group: group, Model: model, Platform: platform, AccountID: r.account.ID, Enabled: true, Priority: r.account.Priority}}, nil
}

func (r *channelServiceRepo) ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error) {
	if r.account == nil {
		return nil, 0, nil
	}
	return []*biz.SubscriptionAccount{r.account}, 1, nil
}

func (r *channelServiceRepo) ListOAuthRefreshCandidates(ctx context.Context, within time.Duration) ([]int64, error) {
	if r.account == nil {
		return nil, nil
	}
	return []int64{r.account.ID}, nil
}

func (r *channelServiceRepo) CreateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	r.createdAccount = account
	account.ID = 201
	return nil
}

func (r *channelServiceRepo) UpdateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	r.updatedAccount = account
	return nil
}

func (r *channelServiceRepo) DeleteSubscriptionAccount(ctx context.Context, accountID int64) error {
	return nil
}

func (r *channelServiceRepo) ChangeSubscriptionAccountStatus(ctx context.Context, accountID int64, status int32) error {
	return nil
}

func (r *channelServiceRepo) SetSubscriptionAccountError(ctx context.Context, accountID int64, message string) error {
	if r.account != nil && r.account.ID == accountID {
		r.account.LastError = message
	}
	return nil
}

func (r *channelServiceRepo) SetTempUnschedulable(ctx context.Context, accountID int64, until time.Time, reason string) error {
	if r.account != nil && r.account.ID == accountID {
		r.account.RateLimitedUntil = until.Unix()
		r.account.LastError = reason
	}
	return nil
}

func (r *channelServiceRepo) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	if r.account != nil && r.account.ID == accountID {
		r.account.RateLimitedUntil = 0
	}
	return nil
}

func (r *channelServiceRepo) RecordAccountQuotaSnapshot(ctx context.Context, snapshot *biz.AccountQuotaSnapshot) error {
	return nil
}

func (r *channelServiceRepo) GetAccountQuotaSnapshot(ctx context.Context, accountID int64) (*biz.AccountQuotaSnapshot, error) {
	return &biz.AccountQuotaSnapshot{AccountID: accountID}, nil
}

func (r *channelServiceRepo) RecordSubscriptionAccountQuotaUsage(ctx context.Context, usage biz.SubscriptionAccountQuotaUsage) error {
	if r.account != nil && r.account.ID == usage.AccountID {
		r.account.QuotaUsedUSD += usage.CostUSD * r.account.EffectiveRateMultiplier()
		r.account.LastUsedAt = usage.OccurredAt.Unix()
	}
	return nil
}

func (r *channelServiceRepo) AggregateSubscriptionAccountQuotaEvents(ctx context.Context, filter biz.SubscriptionAccountQuotaEventFilter) ([]*biz.SubscriptionAccountQuotaEventAggregate, error) {
	return []*biz.SubscriptionAccountQuotaEventAggregate{}, nil
}

func (r *channelServiceRepo) ResetSubscriptionAccountQuota(ctx context.Context, accountID int64, scope string) error {
	if r.account != nil && r.account.ID == accountID {
		r.account.QuotaUsedUSD = 0
		r.account.QuotaDailyUsedUSD = 0
		r.account.QuotaWeeklyUsedUSD = 0
	}
	return nil
}

func (r *channelServiceRepo) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	if r.account != nil && r.account.ID == accountID {
		r.account.Status = biz.ChannelStatusDisabled
		r.account.LastError = reason
	}
	return nil
}

func (r *channelServiceRepo) ClearRecoveryMetadata(ctx context.Context, accountID int64) error {
	if r.account != nil && r.account.ID == accountID {
		r.account.Metadata = ""
		r.account.LastError = ""
	}
	return nil
}

func (r *channelServiceRepo) RecordQuotaResetRun(ctx context.Context, run *biz.SubscriptionAccountQuotaResetRun) error {
	return biz.ErrQuotaResetRunDuplicate
}

func (m *channelServiceRepo) StampQuotaAlertMetadata(ctx context.Context, accountID int64, kind string, alertAt int64) error {
	return nil
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

func (r *channelServiceRepo) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	r.recordedChannelID = channelID
	r.recordedQuota += quota
	return nil
}

func (r *channelServiceRepo) RecordHealth(ctx context.Context, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*biz.Channel, error) {
	r.healthEvent = &event
	return r.channel, nil
}

func (r *channelServiceRepo) DeleteChannel(ctx context.Context, channelID int64) error { return nil }
func (r *channelServiceRepo) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	return nil
}

func TestChannelService_RecordChannelHealth(t *testing.T) {
	repo := &channelServiceRepo{channel: &biz.Channel{ID: 7, Status: biz.ChannelStatusEnabled}}
	svc := NewChannelService(biz.NewChannelUsecase(repo, nil))

	resp, err := svc.RecordChannelHealth(context.Background(), &channelv1.RecordChannelHealthRequest{
		ChannelId:    7,
		Success:      false,
		Error:        "status=502",
		ResponseTime: 321,
	})
	if err != nil {
		t.Fatalf("RecordChannelHealth() error = %v", err)
	}
	if !resp.Success {
		t.Fatalf("RecordChannelHealth() success = false: %s", resp.Message)
	}
	if repo.healthEvent == nil || repo.healthEvent.ChannelID != 7 || repo.healthEvent.Success || repo.healthEvent.Error != "status=502" || repo.healthEvent.ResponseTime != 321 {
		t.Fatalf("unexpected health event: %+v", repo.healthEvent)
	}
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

func TestChannelServiceSubscriptionAccountCRUD(t *testing.T) {
	repo := &channelServiceRepo{
		account: &biz.SubscriptionAccount{
			ID:           11,
			Name:         "codex",
			Platform:     "codex",
			AccountType:  "oauth",
			Status:       biz.ChannelStatusEnabled,
			Group:        "default",
			Models:       []string{"gpt-5"},
			Priority:     9,
			AccountID:    "acc_123",
			AccessToken:  "sk-secret-access-token-value",
			RefreshToken: "rt-secret-refresh-token-value",
		},
	}
	svc := NewChannelService(biz.NewChannelUsecase(repo, nil))

	createResp, err := svc.CreateSubscriptionAccount(context.Background(), &channelv1.CreateSubscriptionAccountRequest{
		Name:        "new",
		Platform:    "claude",
		AccountType: "oauth",
		Group:       "default",
		Models:      "claude-sonnet-4-20250514",
		Priority:    7,
		AccountId:   "acc_456",
	})
	if err != nil || !createResp.Success || repo.createdAccount == nil || repo.createdAccount.ID != 201 {
		t.Fatalf("CreateSubscriptionAccount() mismatch: resp=%+v err=%v account=%+v", createResp, err, repo.createdAccount)
	}

	getResp, err := svc.GetSubscriptionAccount(context.Background(), &channelv1.GetSubscriptionAccountRequest{AccountId: 11})
	if err != nil || getResp.Account == nil || getResp.Account.Name != "codex" {
		t.Fatalf("GetSubscriptionAccount() mismatch: resp=%+v err=%v", getResp, err)
	}
	// H3: the management surface must never return cleartext OAuth tokens.
	if getResp.Account.AccessToken == "sk-secret-access-token-value" || getResp.Account.RefreshToken == "rt-secret-refresh-token-value" {
		t.Fatalf("GetSubscriptionAccount() leaked cleartext tokens: access=%q refresh=%q", getResp.Account.AccessToken, getResp.Account.RefreshToken)
	}

	updateResp, err := svc.UpdateSubscriptionAccount(context.Background(), &channelv1.UpdateSubscriptionAccountRequest{
		Id:     11,
		Name:   "codex-updated",
		Models: "gpt-5-codex",
	})
	if err != nil || !updateResp.Success || repo.updatedAccount == nil || repo.updatedAccount.Name != "codex-updated" {
		t.Fatalf("UpdateSubscriptionAccount() mismatch: resp=%+v err=%v account=%+v", updateResp, err, repo.updatedAccount)
	}
}

func (r *channelServiceRepo) ClearRecoveryMarkers(ctx context.Context, accountID int64, clearTemp, clearError, clearMeta bool) error {
	if r.account != nil && r.account.ID == accountID {
		if clearTemp {
			r.account.RateLimitedUntil = 0
		}
		if clearError {
			r.account.LastError = ""
		}
		if clearMeta {
			r.account.Metadata = ""
		}
	}
	return nil
}
