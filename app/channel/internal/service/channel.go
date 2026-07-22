package service

import (
	"context"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/app/channel/internal/biz"
	relaycredential "micro-one-api/domain/upstream/credential"
	"micro-one-api/pkg/errors"
)

// ChannelService is the transport layer entry for channel-service.
type ChannelService struct {
	channelv1.UnimplementedChannelServiceServer
	uc      *biz.ChannelUsecase
	modelUC *biz.ModelUsecase
}

func NewChannelService(uc *biz.ChannelUsecase) *ChannelService {
	return &ChannelService{uc: uc}
}

// SetModelUsecase wires the optional model-management usecase (方案B).
// When nil, model RPCs return empty/error responses so channel-service
// deployments that have not enabled the model registry keep working.
func (s *ChannelService) SetModelUsecase(uc *biz.ModelUsecase) {
	if s == nil {
		return
	}
	s.modelUC = uc
}

func (s *ChannelService) Usecase() *biz.ChannelUsecase {
	if s == nil {
		return nil
	}
	return s.uc
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

func (s *ChannelService) SelectSubscriptionAccountModel(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*biz.SubscriptionAccount, error) {
	return s.uc.SelectSubscriptionAccount(ctx, group, model, platform, excludeFirstPriority)
}

func (s *ChannelService) GetSubscriptionAccountModel(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error) {
	return s.uc.GetSubscriptionAccount(ctx, accountID)
}

func (s *ChannelService) GetSubscriptionAccountWithSecrets(ctx context.Context, req *channelv1.GetSubscriptionAccountRequest) (*channelv1.GetSubscriptionAccountReply, error) {
	account, err := s.uc.GetSubscriptionAccount(ctx, req.AccountId)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.GetSubscriptionAccountReply{
		Account: toSubscriptionAccountInfoWithSecrets(account, true),
	}, nil
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

func toSubscriptionAccountInfo(account *biz.SubscriptionAccount) *commonv1.SubscriptionAccountInfo {
	return toSubscriptionAccountInfoWithSecrets(account, false)
}

func toSubscriptionAccountInfoWithSecrets(account *biz.SubscriptionAccount, includeSecrets bool) *commonv1.SubscriptionAccountInfo {
	if account == nil {
		return nil
	}
	accessToken := relaycredential.MaskSecret(account.AccessToken)
	refreshToken := relaycredential.MaskSecret(account.RefreshToken)
	if includeSecrets {
		accessToken = account.AccessToken
		refreshToken = account.RefreshToken
	}
	return &commonv1.SubscriptionAccountInfo{
		Id:                     account.ID,
		Name:                   account.Name,
		Platform:               account.Platform,
		AccountType:            account.AccountType,
		Status:                 account.Status,
		Group:                  account.Group,
		Models:                 account.ModelsCSV(),
		Priority:               account.Priority,
		BaseUrl:                account.BaseURL,
		AccessToken:            accessToken,
		RefreshToken:           refreshToken,
		ExpiresAt:              account.ExpiresAt,
		AccountId:              account.AccountID,
		Fingerprint:            account.Fingerprint,
		Metadata:               account.Metadata,
		CreatedAt:              account.CreatedAt,
		UpdatedAt:              account.UpdatedAt,
		Concurrency:            account.Concurrency,
		QuotaLimitUsd:          account.QuotaLimitUSD,
		QuotaUsedUsd:           account.QuotaUsedUSD,
		Quota_5HLimitUsd:       account.Quota5hLimitUSD,
		Quota_5HUsedUsd:        account.Quota5hUsedUSD,
		Quota_5HWindowStart:    account.Quota5hWindowStart,
		QuotaDailyLimitUsd:     account.QuotaDailyLimitUSD,
		QuotaDailyUsedUsd:      account.QuotaDailyUsedUSD,
		QuotaDailyWindowStart:  account.QuotaDailyWindowStart,
		QuotaWeeklyLimitUsd:    account.QuotaWeeklyLimitUSD,
		QuotaWeeklyUsedUsd:     account.QuotaWeeklyUsedUSD,
		QuotaWeeklyWindowStart: account.QuotaWeeklyWindowStart,
		RateMultiplier:         account.RateMultiplier,
		RpmLimit:               account.RPMLimit,
		SessionWindowLimitUsd:  account.SessionWindowLimitUSD,
		QuotaResetStrategy:     account.EffectiveQuotaResetStrategy(),
		QuotaTimezone:          account.EffectiveQuotaTimezone(),
	}
}

func toSubscriptionAccountSummary(account *biz.SubscriptionAccount) *commonv1.SubscriptionAccountSummary {
	if account == nil {
		return nil
	}
	info := account.RecoveryInfo(time.Now())
	return &commonv1.SubscriptionAccountSummary{
		Id:                              account.ID,
		Name:                            account.Name,
		Platform:                        account.Platform,
		AccountType:                     account.AccountType,
		Status:                          account.Status,
		Group:                           account.Group,
		Models:                          account.ModelsCSV(),
		Priority:                        account.Priority,
		AccountId:                       account.AccountID,
		ExpiresAt:                       account.ExpiresAt,
		UpdatedAt:                       account.UpdatedAt,
		LastUsedAt:                      account.LastUsedAt,
		RateLimitedUntil:                account.RateLimitedUntil,
		QuotaUsedPercent:                account.QuotaUsedPercent,
		QuotaResetAt:                    account.QuotaResetAt,
		Concurrency:                     account.Concurrency,
		PrimaryQuotaUsedPercent:         account.PrimaryQuotaUsedPercent,
		PrimaryQuotaResetAfterSeconds:   account.PrimaryQuotaResetAfterSeconds,
		PrimaryQuotaWindowMinutes:       account.PrimaryQuotaWindowMinutes,
		SecondaryQuotaUsedPercent:       account.SecondaryQuotaUsedPercent,
		SecondaryQuotaResetAfterSeconds: account.SecondaryQuotaResetAfterSeconds,
		SecondaryQuotaWindowMinutes:     account.SecondaryQuotaWindowMinutes,
		PrimaryOverSecondaryPercent:     account.PrimaryOverSecondaryPercent,
		QuotaSnapshotUpdatedAt:          account.QuotaSnapshotUpdatedAt,
		QuotaSnapshotPaused:             account.QuotaSnapshotPaused,
		QuotaLimitUsd:                   account.QuotaLimitUSD,
		QuotaUsedUsd:                    account.QuotaUsedUSD,
		Quota_5HLimitUsd:                account.Quota5hLimitUSD,
		Quota_5HUsedUsd:                 account.Quota5hUsedUSD,
		Quota_5HWindowStart:             account.Quota5hWindowStart,
		QuotaDailyLimitUsd:              account.QuotaDailyLimitUSD,
		QuotaDailyUsedUsd:               account.QuotaDailyUsedUSD,
		QuotaDailyWindowStart:           account.QuotaDailyWindowStart,
		QuotaWeeklyLimitUsd:             account.QuotaWeeklyLimitUSD,
		QuotaWeeklyUsedUsd:              account.QuotaWeeklyUsedUSD,
		QuotaWeeklyWindowStart:          account.QuotaWeeklyWindowStart,
		RateMultiplier:                  account.RateMultiplier,
		RpmLimit:                        account.RPMLimit,
		SessionWindowLimitUsd:           account.SessionWindowLimitUSD,
		QuotaResetStrategy:              account.EffectiveQuotaResetStrategy(),
		QuotaTimezone:                   account.EffectiveQuotaTimezone(),
		UnschedulableReason:             info.Reason,
		RecoveryPolicy:                  info.Policy,
		ExpectedRecoveryAt:              info.ExpectedRecoveryAt,
		UnschedulableSince:              info.Since,
	}
}

func toChannelInfo(channel *biz.Channel) *commonv1.ChannelInfo {
	if channel == nil {
		return nil
	}
	return &commonv1.ChannelInfo{
		Id:                                channel.ID,
		Type:                              channel.Type,
		Name:                              channel.Name,
		Status:                            channel.Status,
		BaseUrl:                           channel.BaseURL,
		Group:                             channel.Group,
		Models:                            channel.ModelsCSV(),
		Priority:                          channel.Priority,
		Key:                               channel.Key,
		Weight:                            channel.Weight,
		TestTime:                          channel.TestTime,
		ResponseTime:                      channel.ResponseTime,
		Balance:                           channel.Balance,
		BalanceUpdatedTime:                channel.BalanceUpdatedTime,
		BalanceRefreshLastError:           channel.BalanceRefreshLastError,
		BalanceRefreshLastSuccessTime:     channel.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: channel.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      channel.HealthStatus,
		HealthLastError:                   channel.HealthLastError,
		HealthLastSuccessTime:             channel.HealthLastSuccessTime,
		HealthLastFailureTime:             channel.HealthLastFailureTime,
		HealthConsecutiveFailures:         channel.HealthConsecutiveFailures,
		CircuitOpenedUntil:                channel.CircuitOpenedUntil,
		UsedQuota:                         channel.UsedQuota,
		ModelMapping:                      channel.ModelMapping,
		SystemPrompt:                      channel.SystemPrompt,
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
		HealthStatus:       channel.HealthStatus,
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

func (s *ChannelService) SelectSubscriptionAccount(ctx context.Context, req *channelv1.SelectSubscriptionAccountRequest) (*channelv1.SelectSubscriptionAccountReply, error) {
	account, err := s.uc.SelectSubscriptionAccount(ctx, req.Group, req.Model, req.Platform, req.ExcludeFirstPriority)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.SelectSubscriptionAccountReply{
		Account: toSubscriptionAccountInfo(account),
	}, nil
}

func (s *ChannelService) GetSubscriptionAccount(ctx context.Context, req *channelv1.GetSubscriptionAccountRequest) (*channelv1.GetSubscriptionAccountReply, error) {
	account, err := s.uc.GetSubscriptionAccount(ctx, req.AccountId)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.GetSubscriptionAccountReply{
		Account: toSubscriptionAccountInfo(account),
	}, nil
}

func (s *ChannelService) ListSubscriptionAccounts(ctx context.Context, req *channelv1.ListSubscriptionAccountsRequest) (*channelv1.ListSubscriptionAccountsResponse, error) {
	accounts, total, err := s.uc.ListSubscriptionAccounts(ctx, req.Page, req.PageSize, req.Keyword, req.Group, req.Status, req.Platform)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	result := make([]*commonv1.SubscriptionAccountSummary, len(accounts))
	for i, account := range accounts {
		result[i] = toSubscriptionAccountSummary(account)
	}
	return &channelv1.ListSubscriptionAccountsResponse{
		Accounts: result,
		Total:    total,
	}, nil
}

func (s *ChannelService) ListOAuthRefreshCandidates(ctx context.Context, req *channelv1.ListOAuthRefreshCandidatesRequest) (*channelv1.ListOAuthRefreshCandidatesResponse, error) {
	within := time.Duration(req.GetWithinSeconds()) * time.Second
	if within <= 0 {
		within = 24 * time.Hour
	}
	ids, err := s.uc.ListOAuthRefreshCandidates(ctx, within)
	if err != nil {
		mappedErr := errors.MapChannelError(err)
		return nil, mappedErr
	}
	return &channelv1.ListOAuthRefreshCandidatesResponse{AccountIds: ids}, nil
}

func (s *ChannelService) ClearSubscriptionAccountError(ctx context.Context, req *channelv1.ClearSubscriptionAccountErrorRequest) (*channelv1.ClearSubscriptionAccountErrorResponse, error) {
	if err := s.uc.ClearSubscriptionAccountError(ctx, req.GetAccountId()); err != nil {
		return &channelv1.ClearSubscriptionAccountErrorResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.ClearSubscriptionAccountErrorResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) SetSubscriptionAccountError(ctx context.Context, req *channelv1.SetSubscriptionAccountErrorRequest) (*channelv1.SetSubscriptionAccountErrorResponse, error) {
	if err := s.uc.SetSubscriptionAccountError(ctx, req.GetAccountId(), req.GetMessage()); err != nil {
		return &channelv1.SetSubscriptionAccountErrorResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.SetSubscriptionAccountErrorResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) SetTempUnschedulable(ctx context.Context, req *channelv1.SetTempUnschedulableRequest) (*channelv1.SetTempUnschedulableResponse, error) {
	if err := s.uc.SetTempUnschedulable(ctx, req.GetAccountId(), time.Unix(req.GetUntil(), 0), req.GetReason()); err != nil {
		return &channelv1.SetTempUnschedulableResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.SetTempUnschedulableResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) ClearTempUnschedulable(ctx context.Context, req *channelv1.ClearTempUnschedulableRequest) (*channelv1.ClearTempUnschedulableResponse, error) {
	if err := s.uc.ClearTempUnschedulable(ctx, req.GetAccountId()); err != nil {
		return &channelv1.ClearTempUnschedulableResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.ClearTempUnschedulableResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) CreateSubscriptionAccount(ctx context.Context, req *channelv1.CreateSubscriptionAccountRequest) (*channelv1.CreateSubscriptionAccountResponse, error) {
	account := &biz.SubscriptionAccount{
		Name:                   req.Name,
		Platform:               req.Platform,
		AccountType:            req.AccountType,
		Group:                  req.Group,
		Models:                 biz.SplitCSV(req.Models),
		Priority:               req.Priority,
		BaseURL:                req.BaseUrl,
		AccessToken:            req.AccessToken,
		RefreshToken:           req.RefreshToken,
		ExpiresAt:              req.ExpiresAt,
		AccountID:              req.AccountId,
		Fingerprint:            req.Fingerprint,
		Metadata:               req.Metadata,
		Status:                 biz.ChannelStatusEnabled,
		QuotaLimitUSD:          req.QuotaLimitUsd,
		QuotaUsedUSD:           req.QuotaUsedUsd,
		Quota5hLimitUSD:        req.Quota_5HLimitUsd,
		Quota5hUsedUSD:         req.Quota_5HUsedUsd,
		Quota5hWindowStart:     req.Quota_5HWindowStart,
		QuotaDailyLimitUSD:     req.QuotaDailyLimitUsd,
		QuotaDailyUsedUSD:      req.QuotaDailyUsedUsd,
		QuotaDailyWindowStart:  req.QuotaDailyWindowStart,
		QuotaWeeklyLimitUSD:    req.QuotaWeeklyLimitUsd,
		QuotaWeeklyUsedUSD:     req.QuotaWeeklyUsedUsd,
		QuotaWeeklyWindowStart: req.QuotaWeeklyWindowStart,
		RateMultiplier:         req.RateMultiplier,
		RPMLimit:               req.RpmLimit,
		SessionWindowLimitUSD:  req.SessionWindowLimitUsd,
		QuotaResetStrategy:     req.QuotaResetStrategy,
		QuotaTimezone:          req.QuotaTimezone,
	}
	if err := s.uc.CreateSubscriptionAccount(ctx, account); err != nil {
		return &channelv1.CreateSubscriptionAccountResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.CreateSubscriptionAccountResponse{
		Success:   true,
		Message:   "ok",
		AccountId: account.ID,
	}, nil
}

func (s *ChannelService) UpdateSubscriptionAccount(ctx context.Context, req *channelv1.UpdateSubscriptionAccountRequest) (*channelv1.UpdateSubscriptionAccountResponse, error) {
	account, err := s.uc.GetSubscriptionAccount(ctx, req.Id)
	if err != nil {
		return &channelv1.UpdateSubscriptionAccountResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	if req.Name != "" {
		account.Name = req.Name
	}
	if req.AccountType != "" {
		account.AccountType = req.AccountType
	}
	if req.Group != "" {
		account.Group = req.Group
	}
	if req.Models != "" {
		account.Models = biz.SplitCSV(req.Models)
	}
	if req.Priority != 0 {
		account.Priority = req.Priority
	}
	if req.BaseUrl != "" {
		account.BaseURL = req.BaseUrl
	}
	if req.AccessToken != "" {
		account.AccessToken = req.AccessToken
	}
	if req.RefreshToken != "" {
		account.RefreshToken = req.RefreshToken
	}
	if req.ExpiresAt != 0 {
		account.ExpiresAt = req.ExpiresAt
	}
	if req.AccountId != "" {
		account.AccountID = req.AccountId
	}
	if req.Fingerprint != "" {
		account.Fingerprint = req.Fingerprint
	}
	if req.Metadata != "" {
		account.Metadata = req.Metadata
	}
	if req.QuotaLimitUsd != nil {
		account.QuotaLimitUSD = req.GetQuotaLimitUsd()
	}
	if req.QuotaUsedUsd != nil {
		account.QuotaUsedUSD = req.GetQuotaUsedUsd()
	}
	if req.Quota_5HLimitUsd != nil {
		account.Quota5hLimitUSD = req.GetQuota_5HLimitUsd()
	}
	if req.Quota_5HUsedUsd != nil {
		account.Quota5hUsedUSD = req.GetQuota_5HUsedUsd()
	}
	if req.Quota_5HWindowStart != nil {
		account.Quota5hWindowStart = req.GetQuota_5HWindowStart()
	}
	if req.QuotaDailyLimitUsd != nil {
		account.QuotaDailyLimitUSD = req.GetQuotaDailyLimitUsd()
	}
	if req.QuotaDailyUsedUsd != nil {
		account.QuotaDailyUsedUSD = req.GetQuotaDailyUsedUsd()
	}
	if req.QuotaDailyWindowStart != nil {
		account.QuotaDailyWindowStart = req.GetQuotaDailyWindowStart()
	}
	if req.QuotaWeeklyLimitUsd != nil {
		account.QuotaWeeklyLimitUSD = req.GetQuotaWeeklyLimitUsd()
	}
	if req.QuotaWeeklyUsedUsd != nil {
		account.QuotaWeeklyUsedUSD = req.GetQuotaWeeklyUsedUsd()
	}
	if req.QuotaWeeklyWindowStart != nil {
		account.QuotaWeeklyWindowStart = req.GetQuotaWeeklyWindowStart()
	}
	if req.RateMultiplier != nil {
		account.RateMultiplier = req.GetRateMultiplier()
	}
	if req.RpmLimit != nil {
		account.RPMLimit = req.GetRpmLimit()
	}
	if req.SessionWindowLimitUsd != nil {
		account.SessionWindowLimitUSD = req.GetSessionWindowLimitUsd()
	}
	if req.QuotaResetStrategy != nil {
		account.QuotaResetStrategy = req.GetQuotaResetStrategy()
	}
	if req.QuotaTimezone != nil {
		account.QuotaTimezone = req.GetQuotaTimezone()
	}
	if err := s.uc.UpdateSubscriptionAccount(ctx, account); err != nil {
		return &channelv1.UpdateSubscriptionAccountResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.UpdateSubscriptionAccountResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *ChannelService) DeleteSubscriptionAccount(ctx context.Context, req *channelv1.DeleteSubscriptionAccountRequest) (*channelv1.DeleteSubscriptionAccountResponse, error) {
	if err := s.uc.DeleteSubscriptionAccount(ctx, req.AccountId); err != nil {
		return &channelv1.DeleteSubscriptionAccountResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.DeleteSubscriptionAccountResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *ChannelService) ChangeSubscriptionAccountStatus(ctx context.Context, req *channelv1.ChangeSubscriptionAccountStatusRequest) (*channelv1.ChangeSubscriptionAccountStatusResponse, error) {
	if err := s.uc.ChangeSubscriptionAccountStatus(ctx, req.AccountId, req.Status); err != nil {
		return &channelv1.ChangeSubscriptionAccountStatusResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.ChangeSubscriptionAccountStatusResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *ChannelService) RecordAccountQuotaSnapshot(ctx context.Context, req *channelv1.RecordAccountQuotaSnapshotRequest) (*channelv1.RecordAccountQuotaSnapshotResponse, error) {
	snapshot := &biz.AccountQuotaSnapshot{
		AccountID:      req.GetAccountId(),
		SnapshotPaused: req.GetSnapshotPaused(),
	}
	if req.PrimaryUsedPercent != nil {
		v := req.GetPrimaryUsedPercent()
		snapshot.PrimaryUsedPercent = &v
	}
	if req.PrimaryResetAfterSeconds != nil {
		v := req.GetPrimaryResetAfterSeconds()
		snapshot.PrimaryResetAfterSeconds = &v
	}
	if req.PrimaryWindowMinutes != nil {
		v := req.GetPrimaryWindowMinutes()
		snapshot.PrimaryWindowMinutes = &v
	}
	if req.SecondaryUsedPercent != nil {
		v := req.GetSecondaryUsedPercent()
		snapshot.SecondaryUsedPercent = &v
	}
	if req.SecondaryResetAfterSeconds != nil {
		v := req.GetSecondaryResetAfterSeconds()
		snapshot.SecondaryResetAfterSeconds = &v
	}
	if req.SecondaryWindowMinutes != nil {
		v := req.GetSecondaryWindowMinutes()
		snapshot.SecondaryWindowMinutes = &v
	}
	if req.PrimaryOverSecondaryPercent != nil {
		v := req.GetPrimaryOverSecondaryPercent()
		snapshot.PrimaryOverSecondaryPercent = &v
	}
	if req.GetUpdatedAt() > 0 {
		snapshot.UpdatedAt = time.Unix(req.GetUpdatedAt(), 0)
	}
	if err := s.uc.RecordAccountQuotaSnapshot(ctx, snapshot); err != nil {
		return &channelv1.RecordAccountQuotaSnapshotResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.RecordAccountQuotaSnapshotResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) RecordSubscriptionAccountQuotaUsage(ctx context.Context, req *channelv1.RecordSubscriptionAccountQuotaUsageRequest) (*channelv1.RecordSubscriptionAccountQuotaUsageResponse, error) {
	occurredAt := time.Time{}
	if req.GetOccurredAt() > 0 {
		occurredAt = time.Unix(req.GetOccurredAt(), 0)
	}
	if err := s.uc.RecordSubscriptionAccountQuotaUsage(ctx, biz.SubscriptionAccountQuotaUsage{
		AccountID:     req.GetAccountId(),
		ReservationID: req.GetReservationId(),
		CostSource:    req.GetCostSource(),
		CostUSD:       req.GetCostUsd(),
		OccurredAt:    occurredAt,
	}); err != nil {
		return &channelv1.RecordSubscriptionAccountQuotaUsageResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.RecordSubscriptionAccountQuotaUsageResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) AggregateSubscriptionAccountQuotaEvents(ctx context.Context, req *channelv1.AggregateSubscriptionAccountQuotaEventsRequest) (*channelv1.AggregateSubscriptionAccountQuotaEventsResponse, error) {
	filter := biz.SubscriptionAccountQuotaEventFilter{
		AccountID: req.GetAccountId(),
		Limit:     int(req.GetLimit()),
	}
	if req.GetStartTime() > 0 {
		filter.StartTime = time.Unix(req.GetStartTime(), 0)
	}
	if req.GetEndTime() > 0 {
		filter.EndTime = time.Unix(req.GetEndTime(), 0)
	}
	items, err := s.uc.AggregateSubscriptionAccountQuotaEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp := &channelv1.AggregateSubscriptionAccountQuotaEventsResponse{Items: make([]*channelv1.SubscriptionAccountQuotaEventAggregate, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, &channelv1.SubscriptionAccountQuotaEventAggregate{
			SubscriptionAccountId: item.SubscriptionAccountID,
			CostUsd:               item.CostUSD,
			ChargedUsd:            item.ChargedUSD,
			AverageRateMultiplier: item.AverageRateMultiplier,
			Count:                 item.Count,
			LastOccurredAt:        item.LastOccurredAt,
		})
	}
	return resp, nil
}

func (s *ChannelService) ResetSubscriptionAccountQuota(ctx context.Context, req *channelv1.ResetSubscriptionAccountQuotaRequest) (*channelv1.ResetSubscriptionAccountQuotaResponse, error) {
	if err := s.uc.ResetSubscriptionAccountQuota(ctx, req.GetAccountId(), req.GetScope()); err != nil {
		return &channelv1.ResetSubscriptionAccountQuotaResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.ResetSubscriptionAccountQuotaResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) CreateChannel(ctx context.Context, req *channelv1.CreateChannelRequest) (*channelv1.CreateChannelResponse, error) {
	// Read config fields by accessors on the pointer rather than copying the
	// protobuf value (it embeds protoimpl.MessageState which contains a mutex).
	var apiVersion, region, libraryID, plugin, vertexProjectID string
	if req.Config != nil {
		apiVersion = req.Config.GetApiVersion()
		region = req.Config.GetRegion()
		libraryID = req.Config.GetLibraryId()
		plugin = req.Config.GetPlugin()
		vertexProjectID = req.Config.GetVertexAiProjectId()
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
			APIVersion:        apiVersion,
			Region:            region,
			LibraryID:         libraryID,
			Plugin:            plugin,
			VertexAIProjectID: vertexProjectID,
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
	if req.Balance != 0 || req.BalanceUpdatedTime != 0 {
		channel.Balance = req.Balance
		channel.BalanceUpdatedTime = req.BalanceUpdatedTime
	}
	if req.SetBalanceRefreshFields {
		channel.BalanceRefreshLastError = req.BalanceRefreshLastError
		channel.BalanceRefreshLastSuccessTime = req.BalanceRefreshLastSuccessTime
		channel.ConsecutiveBalanceRefreshFailures = req.ConsecutiveBalanceRefreshFailures
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

func (s *ChannelService) RecordChannelUsage(ctx context.Context, req *channelv1.RecordChannelUsageRequest) (*channelv1.RecordChannelUsageResponse, error) {
	if err := s.uc.RecordUsage(ctx, req.ChannelId, req.Quota); err != nil {
		return &channelv1.RecordChannelUsageResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.RecordChannelUsageResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *ChannelService) RecordChannelHealth(ctx context.Context, req *channelv1.RecordChannelHealthRequest) (*channelv1.RecordChannelHealthResponse, error) {
	event := biz.ChannelHealthEvent{
		ChannelID:    req.ChannelId,
		Success:      req.Success,
		Error:        req.Error,
		ResponseTime: req.ResponseTime,
	}
	if err := s.uc.RecordHealth(ctx, event); err != nil {
		return &channelv1.RecordChannelHealthResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &channelv1.RecordChannelHealthResponse{
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
