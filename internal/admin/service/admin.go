package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AdminService is the transport layer entry for admin-api.
type AdminService struct {
	adminv1.UnimplementedAdminServiceServer
	billingClient  billingv1.BillingServiceClient
	identityClient identityv1.IdentityServiceClient
	channelClient  channelv1.ChannelServiceClient
	systemOptsRepo SystemOptionsStore
	httpClient     *http.Client
}

// SystemOptionsStore is the interface for system options persistence.
type SystemOptionsStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

type OneAPIOption struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NewAdminService creates a new admin service
func NewAdminService(
	billingClient billingv1.BillingServiceClient,
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	systemOptsRepo SystemOptionsStore,
) *AdminService {
	return &AdminService{
		billingClient:  billingClient,
		identityClient: identityClient,
		channelClient:  channelClient,
		systemOptsRepo: systemOptsRepo,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
	}
}

// TopUpQuota 充值
func (s *AdminService) TopUpQuota(ctx context.Context, req *adminv1.TopUpQuotaRequest) (*adminv1.TopUpQuotaResponse, error) {
	billingReq := &billingv1.TopUpQuotaRequest{
		UserId:     req.UserId,
		Amount:     req.Amount,
		OperatorId: req.OperatorId,
		Remark:     req.Remark,
	}

	resp, err := s.billingClient.TopUpQuota(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return &adminv1.TopUpQuotaResponse{
				Success:      false,
				ErrorMessage: st.Message(),
			}, nil
		}
		return &adminv1.TopUpQuotaResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &adminv1.TopUpQuotaResponse{
		Success:  true,
		NewQuota: resp.NewQuota,
	}, nil
}

// CreateRedeemCode 创建兑换码
func (s *AdminService) CreateRedeemCode(ctx context.Context, req *adminv1.CreateRedeemCodeRequest) (*adminv1.CreateRedeemCodeResponse, error) {
	billingReq := &billingv1.CreateRedeemCodeRequest{
		Code:       req.Code,
		Name:       req.Name,
		Amount:     req.Amount,
		Count:      req.Count,
		OperatorId: req.OperatorId,
	}

	_, err := s.billingClient.CreateRedeemCode(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return &adminv1.CreateRedeemCodeResponse{
				Success:      false,
				ErrorMessage: st.Message(),
			}, nil
		}
		return &adminv1.CreateRedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &adminv1.CreateRedeemCodeResponse{
		Success: true,
	}, nil
}

// CreateRedeemCodesBatch 批量创建兑换码
func (s *AdminService) CreateRedeemCodesBatch(ctx context.Context, req *adminv1.CreateRedeemCodesBatchRequest) (*adminv1.CreateRedeemCodesBatchResponse, error) {
	billingReq := &billingv1.CreateRedeemCodesBatchRequest{
		Name:       req.Name,
		Amount:     req.Amount,
		Count:      req.Count,
		BatchSize:  req.BatchSize,
		OperatorId: req.OperatorId,
	}

	resp, err := s.billingClient.CreateRedeemCodesBatch(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return &adminv1.CreateRedeemCodesBatchResponse{
				Success:      false,
				ErrorMessage: st.Message(),
			}, nil
		}
		return &adminv1.CreateRedeemCodesBatchResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &adminv1.CreateRedeemCodesBatchResponse{
		Success: resp.Success,
		Codes:   resp.Codes,
	}, nil
}

// GetRedeemCode 获取兑换码
func (s *AdminService) GetRedeemCode(ctx context.Context, req *adminv1.GetRedeemCodeRequest) (*adminv1.RedeemCodeResponse, error) {
	billingReq := &billingv1.GetRedeemCodeRequest{
		Code: req.Code,
	}

	resp, err := s.billingClient.GetRedeemCode(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			if st.Code() == codes.NotFound {
				return &adminv1.RedeemCodeResponse{
					ErrorMessage: "redeem code not found",
				}, nil
			}
			return nil, err
		}
		return nil, err
	}

	redeemCodeInfo := &adminv1.RedeemCodeInfo{
		Code:      resp.RedeemCode.Code,
		Name:      resp.RedeemCode.Name,
		Amount:    resp.RedeemCode.Amount,
		Count:     resp.RedeemCode.Count,
		Status:    resp.RedeemCode.Status,
		CreatedBy: resp.RedeemCode.CreatedBy,
		CreatedAt: resp.RedeemCode.CreatedAt.AsTime().Unix(),
	}

	return &adminv1.RedeemCodeResponse{
		RedeemCode: redeemCodeInfo,
	}, nil
}

// ListRedeemCodes 获取兑换码列表
func (s *AdminService) ListRedeemCodes(ctx context.Context, req *adminv1.ListRedeemCodesRequest) (*adminv1.ListRedeemCodesResponse, error) {
	billingReq := &billingv1.ListRedeemCodesRequest{
		Page:     req.Page,
		PageSize: req.PageSize,
	}

	resp, err := s.billingClient.ListRedeemCodes(ctx, billingReq)
	if err != nil {
		return &adminv1.ListRedeemCodesResponse{
			Codes: []*adminv1.RedeemCodeInfo{},
			Total: 0,
		}, nil
	}

	codes := make([]*adminv1.RedeemCodeInfo, len(resp.Codes))
	for i, code := range resp.Codes {
		codes[i] = &adminv1.RedeemCodeInfo{
			Code:      code.Code,
			Name:      code.Name,
			Amount:    code.Amount,
			Count:     code.Count,
			Status:    code.Status,
			CreatedBy: code.CreatedBy,
			CreatedAt: code.CreatedAt.AsTime().Unix(),
		}
	}

	return &adminv1.ListRedeemCodesResponse{
		Codes: codes,
		Total: resp.Total,
	}, nil
}

// SearchRedeemCodes 搜索兑换码
func (s *AdminService) SearchRedeemCodes(ctx context.Context, req *adminv1.SearchRedeemCodesRequest) (*adminv1.RedeemCodesSearchResponse, error) {
	billingReq := &billingv1.SearchRedeemCodesRequest{
		Keyword: req.Keyword,
	}

	resp, err := s.billingClient.SearchRedeemCodes(ctx, billingReq)
	if err != nil {
		return &adminv1.RedeemCodesSearchResponse{
			Codes: []*adminv1.RedeemCodeInfo{},
		}, nil
	}

	codes := make([]*adminv1.RedeemCodeInfo, len(resp.Codes))
	for i, code := range resp.Codes {
		codes[i] = &adminv1.RedeemCodeInfo{
			Code:      code.Code,
			Name:      code.Name,
			Amount:    code.Amount,
			Count:     code.Count,
			Status:    code.Status,
			CreatedBy: code.CreatedBy,
			CreatedAt: code.CreatedAt.AsTime().Unix(),
		}
	}

	return &adminv1.RedeemCodesSearchResponse{
		Codes: codes,
	}, nil
}

// UpdateRedeemCode 更新兑换码
func (s *AdminService) UpdateRedeemCode(ctx context.Context, req *adminv1.UpdateRedeemCodeRequest) (*adminv1.UpdateRedeemCodeResponse, error) {
	billingReq := &billingv1.UpdateRedeemCodeRequest{
		Code:   req.Code,
		Name:   req.Name,
		Amount: req.Amount,
		Status: req.Status,
	}

	_, err := s.billingClient.UpdateRedeemCode(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return &adminv1.UpdateRedeemCodeResponse{
				Success:      false,
				ErrorMessage: st.Message(),
			}, nil
		}
		return &adminv1.UpdateRedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &adminv1.UpdateRedeemCodeResponse{
		Success: true,
	}, nil
}

// DeleteRedeemCode 删除兑换码
func (s *AdminService) DeleteRedeemCode(ctx context.Context, req *adminv1.DeleteRedeemCodeRequest) (*adminv1.DeleteRedeemCodeResponse, error) {
	billingReq := &billingv1.DeleteRedeemCodeRequest{
		Code: req.Code,
	}

	_, err := s.billingClient.DeleteRedeemCode(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return &adminv1.DeleteRedeemCodeResponse{
				Success:      false,
				ErrorMessage: st.Message(),
			}, nil
		}
		return &adminv1.DeleteRedeemCodeResponse{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &adminv1.DeleteRedeemCodeResponse{
		Success: true,
	}, nil
}

// ListUserLedger 查询用户流水
func (s *AdminService) ListUserLedger(ctx context.Context, req *adminv1.ListUserLedgerRequest) (*adminv1.UserLedgerResponse, error) {
	billingReq := &billingv1.ListLedgerRequest{
		UserId:   req.UserId,
		Page:     req.Page,
		PageSize: req.PageSize,
	}

	resp, err := s.billingClient.ListLedger(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			if st.Code() == codes.NotFound {
				return &adminv1.UserLedgerResponse{
					Ledgers: []*commonv1.LedgerInfo{},
					Total:   0,
				}, nil
			}
			return nil, err
		}
		return nil, err
	}

	ledgers := make([]*commonv1.LedgerInfo, len(resp.Entries))
	for i, ledger := range resp.Entries {
		ledgers[i] = &commonv1.LedgerInfo{
			Id:           0,
			UserId:       ledger.UserId,
			Amount:       ledger.Amount,
			BalanceAfter: ledger.BalanceAfter,
			Type:         ledger.Type,
			ReferenceId:  ledger.ReferenceId,
			Remark:       ledger.Remark,
			CreatedAt:    ledger.CreatedAt.AsTime().Unix(),
		}
	}

	return &adminv1.UserLedgerResponse{
		Ledgers: ledgers,
		Total:   resp.Total,
	}, nil
}

// GetAccountSnapshot 获取账户快照
func (s *AdminService) GetAccountSnapshot(ctx context.Context, req *adminv1.GetAccountSnapshotRequest) (*adminv1.AdminAccountSnapshotResponse, error) {
	billingReq := &billingv1.GetAccountSnapshotRequest{
		UserId: req.UserId,
	}

	resp, err := s.billingClient.GetAccountSnapshot(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			if st.Code() == codes.NotFound {
				return nil, status.Error(codes.NotFound, "account not found")
			}
			return nil, err
		}
		return nil, err
	}

	return &adminv1.AdminAccountSnapshotResponse{
		Account: &commonv1.AccountInfo{
			UserId:       resp.Snapshot.UserId,
			Username:     "",
			DisplayName:  "",
			Group:        resp.Snapshot.Group,
			Quota:        resp.Snapshot.Quota,
			UsedQuota:    resp.Snapshot.UsedQuota,
			RequestCount: resp.Snapshot.RequestCount,
			FrozenQuota:  resp.Snapshot.FrozenQuota,
			Status:       0,
		},
	}, nil
}

// ========== 用户管理 ==========

func (s *AdminService) ListUsers(ctx context.Context, req *adminv1.AdminListUsersRequest) (*adminv1.AdminListUsersResponse, error) {
	resp, err := s.identityClient.ListUsers(ctx, &identityv1.ListUsersRequest{
		Page:     req.Page,
		PageSize: req.PageSize,
		Keyword:  req.Keyword,
		Group:    req.Group,
		Status:   req.Status,
	})
	if err != nil {
		return &adminv1.AdminListUsersResponse{Users: []*commonv1.UserInfo{}, Total: 0}, nil
	}
	return &adminv1.AdminListUsersResponse{
		Users: resp.Users,
		Total: resp.Total,
	}, nil
}

func (s *AdminService) GetUser(ctx context.Context, userID int64) (*commonv1.UserInfo, error) {
	resp, err := s.identityClient.GetUser(ctx, &identityv1.GetUserRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.User == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	return resp.User, nil
}

func (s *AdminService) CreateUser(ctx context.Context, req *adminv1.AdminCreateUserRequest) (*adminv1.AdminCreateUserResponse, error) {
	resp, err := s.identityClient.CreateUser(ctx, &identityv1.CreateUserRequest{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Password:    req.Password,
		Group:       req.Group,
		Quota:       req.Quota,
	})
	if err != nil {
		return &adminv1.AdminCreateUserResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminCreateUserResponse{
		Success: resp.Success,
		Message: resp.Message,
		UserId:  resp.UserId,
	}, nil
}

func (s *AdminService) UpdateUser(ctx context.Context, req *adminv1.AdminUpdateUserRequest) (*adminv1.AdminUpdateUserResponse, error) {
	resp, err := s.identityClient.UpdateUser(ctx, &identityv1.UpdateUserRequest{
		UserId:      req.UserId,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Group:       req.Group,
		Status:      req.Status,
	})
	if err != nil {
		return &adminv1.AdminUpdateUserResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminUpdateUserResponse{Success: resp.Success, Message: resp.Message}, nil
}

func (s *AdminService) DeleteUser(ctx context.Context, req *adminv1.AdminDeleteUserRequest) (*adminv1.AdminDeleteUserResponse, error) {
	resp, err := s.identityClient.DeleteUser(ctx, &identityv1.DeleteUserRequest{UserId: req.UserId})
	if err != nil {
		return &adminv1.AdminDeleteUserResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminDeleteUserResponse{Success: resp.Success, Message: resp.Message}, nil
}

func (s *AdminService) ResetUserQuota(ctx context.Context, req *adminv1.ResetUserQuotaRequest) (*adminv1.ResetUserQuotaResponse, error) {
	snapshot, err := s.billingClient.GetAccountSnapshot(ctx, &billingv1.GetAccountSnapshotRequest{
		UserId: fmt.Sprintf("%d", req.UserId),
	})
	if err != nil {
		return &adminv1.ResetUserQuotaResponse{Success: false, Message: err.Error()}, nil
	}
	currentQuota := int64(0)
	if snapshot != nil && snapshot.Snapshot != nil {
		currentQuota = snapshot.Snapshot.Quota
	}
	delta := req.NewQuota - currentQuota
	if delta == 0 {
		return &adminv1.ResetUserQuotaResponse{Success: true, Message: "ok"}, nil
	}
	_, err = s.billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
		UserId:     fmt.Sprintf("%d", req.UserId),
		Amount:     delta,
		OperatorId: req.OperatorId,
		Remark:     req.Remark,
	})
	if err != nil {
		return &adminv1.ResetUserQuotaResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.ResetUserQuotaResponse{Success: true, Message: "ok"}, nil
}

func (s *AdminService) TestChannel(ctx context.Context, channelID int64) (map[string]interface{}, error) {
	resp, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Channel == nil {
		return nil, status.Error(codes.NotFound, "channel not found")
	}
	return map[string]interface{}{
		"success":    true,
		"channel_id": resp.Channel.Id,
		"name":       resp.Channel.Name,
		"type":       resp.Channel.Type,
		"status":     resp.Channel.Status,
		"group":      resp.Channel.Group,
		"models":     resp.Channel.Models,
		"message":    "channel metadata resolved",
	}, nil
}

// ========== 渠道管理 ==========

func (s *AdminService) ListChannels(ctx context.Context, req *adminv1.AdminListChannelsRequest) (*adminv1.AdminListChannelsResponse, error) {
	resp, err := s.channelClient.ListChannels(ctx, &channelv1.ListChannelsRequest{
		Page:     req.Page,
		PageSize: req.PageSize,
		Keyword:  req.Keyword,
		Group:    req.Group,
		Status:   req.Status,
		Type:     req.Type,
	})
	if err != nil {
		return &adminv1.AdminListChannelsResponse{Channels: []*commonv1.ChannelSummary{}, Total: 0}, nil
	}
	return &adminv1.AdminListChannelsResponse{
		Channels: resp.Channels,
		Total:    resp.Total,
	}, nil
}

func (s *AdminService) GetChannel(ctx context.Context, channelID int64) (*commonv1.ChannelInfo, error) {
	resp, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Channel == nil {
		return nil, status.Error(codes.NotFound, "channel not found")
	}
	return resp.Channel, nil
}

func (s *AdminService) CreateChannel(ctx context.Context, req *adminv1.AdminCreateChannelRequest) (*adminv1.AdminCreateChannelResponse, error) {
	resp, err := s.channelClient.CreateChannel(ctx, &channelv1.CreateChannelRequest{
		Name:         req.Name,
		Type:         req.Type,
		BaseUrl:      req.BaseUrl,
		Key:          req.Key,
		Models:       req.Models,
		Group:        req.Group,
		Priority:     req.Priority,
		Config:       req.Config,
		Weight:       req.Weight,
		ModelMapping: req.ModelMapping,
		SystemPrompt: req.SystemPrompt,
	})
	if err != nil {
		return &adminv1.AdminCreateChannelResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminCreateChannelResponse{
		Success:   resp.Success,
		Message:   resp.Message,
		ChannelId: resp.ChannelId,
	}, nil
}

func (s *AdminService) UpdateChannel(ctx context.Context, req *adminv1.AdminUpdateChannelRequest) (*adminv1.AdminUpdateChannelResponse, error) {
	resp, err := s.channelClient.UpdateChannel(ctx, &channelv1.UpdateChannelRequest{
		ChannelId:    req.ChannelId,
		Name:         req.Name,
		BaseUrl:      req.BaseUrl,
		Key:          req.Key,
		Models:       req.Models,
		Group:        req.Group,
		Priority:     req.Priority,
		Config:       req.Config,
		Weight:       req.Weight,
		ModelMapping: req.ModelMapping,
		SystemPrompt: req.SystemPrompt,
	})
	if err != nil {
		return &adminv1.AdminUpdateChannelResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminUpdateChannelResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

type ChannelBalanceRefreshResult struct {
	Success            bool    `json:"success"`
	ChannelID          int64   `json:"channel_id"`
	Provider           string  `json:"provider,omitempty"`
	Balance            float64 `json:"balance,omitempty"`
	BalanceUpdatedTime int64   `json:"balance_updated_time,omitempty"`
	Message            string  `json:"message,omitempty"`
}

func (s *AdminService) RefreshChannelBalance(ctx context.Context, channelID int64) (*ChannelBalanceRefreshResult, error) {
	channel, err := s.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	return s.refreshChannelBalance(ctx, channel)
}

func (s *AdminService) RefreshAllChannelBalances(ctx context.Context) ([]*ChannelBalanceRefreshResult, error) {
	resp, err := s.channelClient.ListChannels(ctx, &channelv1.ListChannelsRequest{Page: 1, PageSize: 1000, Status: 1})
	if err != nil {
		return nil, err
	}
	results := make([]*ChannelBalanceRefreshResult, 0, len(resp.GetChannels()))
	for _, summary := range resp.GetChannels() {
		channel, err := s.GetChannel(ctx, summary.GetId())
		if err != nil {
			results = append(results, &ChannelBalanceRefreshResult{Success: false, ChannelID: summary.GetId(), Message: err.Error()})
			continue
		}
		result, err := s.refreshChannelBalance(ctx, channel)
		if err != nil {
			results = append(results, &ChannelBalanceRefreshResult{Success: false, ChannelID: summary.GetId(), Message: err.Error()})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *AdminService) refreshChannelBalance(ctx context.Context, channel *commonv1.ChannelInfo) (*ChannelBalanceRefreshResult, error) {
	adapter := balanceAdapterForChannel(channel)
	if adapter == nil {
		return &ChannelBalanceRefreshResult{
			Success:   false,
			ChannelID: channel.GetId(),
			Message:   "unsupported channel balance provider",
		}, nil
	}
	balance, err := adapter.fetch(ctx, s.httpClient, channel)
	if err != nil {
		return &ChannelBalanceRefreshResult{
			Success:   false,
			ChannelID: channel.GetId(),
			Provider:  adapter.name,
			Message:   err.Error(),
		}, nil
	}
	now := time.Now().Unix()
	resp, err := s.channelClient.UpdateChannel(ctx, &channelv1.UpdateChannelRequest{
		ChannelId:          channel.GetId(),
		Balance:            balance,
		BalanceUpdatedTime: now,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetSuccess() {
		message := resp.GetMessage()
		if message == "" {
			message = "failed to persist channel balance"
		}
		return &ChannelBalanceRefreshResult{Success: false, ChannelID: channel.GetId(), Provider: adapter.name, Message: message}, nil
	}
	return &ChannelBalanceRefreshResult{
		Success:            true,
		ChannelID:          channel.GetId(),
		Provider:           adapter.name,
		Balance:            balance,
		BalanceUpdatedTime: now,
	}, nil
}

type channelBalanceAdapter struct {
	name  string
	fetch func(context.Context, *http.Client, *commonv1.ChannelInfo) (float64, error)
}

func balanceAdapterForChannel(channel *commonv1.ChannelInfo) *channelBalanceAdapter {
	host := ""
	if parsed, err := url.Parse(channel.GetBaseUrl()); err == nil {
		host = strings.ToLower(parsed.Host)
	}
	switch {
	case strings.Contains(host, "openrouter"):
		return &channelBalanceAdapter{name: "openrouter_credits", fetch: fetchOpenRouterBalance}
	case strings.Contains(host, "siliconflow"):
		return &channelBalanceAdapter{name: "siliconflow_user_info", fetch: fetchSiliconFlowBalance}
	case strings.Contains(host, "deepseek"):
		return &channelBalanceAdapter{name: "deepseek_balance", fetch: fetchDeepSeekBalance}
	case channel.GetType() == 1:
		return &channelBalanceAdapter{name: "openai_dashboard", fetch: fetchOpenAIDashboardBalance}
	default:
		return nil
	}
}

func fetchOpenAIDashboardBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := trimV1Base(channel.GetBaseUrl()) + "/dashboard/billing/credit_grants"
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.GetKey())
	if err != nil {
		return 0, err
	}
	return firstFloat(payload, "total_available", "total_granted")
}

func fetchOpenRouterBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := trimV1Base(channel.GetBaseUrl()) + "/api/v1/credits"
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.GetKey())
	if err != nil {
		return 0, err
	}
	data, _ := payload["data"].(map[string]interface{})
	if total, ok := floatFromMap(data, "total_credits"); ok {
		if used, usedOK := floatFromMap(data, "total_usage"); usedOK {
			return total - used, nil
		}
		return total, nil
	}
	return firstFloat(payload, "total_available", "balance")
}

func fetchSiliconFlowBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := strings.TrimRight(channel.GetBaseUrl(), "/") + "/user/info"
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.GetKey())
	if err != nil {
		return 0, err
	}
	if data, _ := payload["data"].(map[string]interface{}); data != nil {
		if balance, ok := floatFromMap(data, "balance"); ok {
			return balance, nil
		}
	}
	return firstFloat(payload, "balance", "total_available")
}

func fetchDeepSeekBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := trimV1Base(channel.GetBaseUrl()) + "/user/balance"
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.GetKey())
	if err != nil {
		return 0, err
	}
	if infos, ok := payload["balance_infos"].([]interface{}); ok {
		total := 0.0
		for _, item := range infos {
			info, _ := item.(map[string]interface{})
			if balance, ok := floatFromMap(info, "total_balance"); ok {
				total += balance
			}
		}
		return total, nil
	}
	return firstFloat(payload, "balance", "total_available")
}

func fetchBalancePayload(ctx context.Context, client *http.Client, endpoint, key string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("balance upstream returned status %d", resp.StatusCode)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func trimV1Base(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	return strings.TrimSuffix(base, "/v1")
}

func firstFloat(payload map[string]interface{}, keys ...string) (float64, error) {
	for _, key := range keys {
		if value, ok := floatFromMap(payload, key); ok {
			return value, nil
		}
	}
	return 0, fmt.Errorf("balance field not found")
}

func floatFromMap(payload map[string]interface{}, key string) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	switch value := payload[key].(type) {
	case float64:
		return value, true
	case int64:
		return float64(value), true
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *AdminService) DeleteChannel(ctx context.Context, req *adminv1.AdminDeleteChannelRequest) (*adminv1.AdminDeleteChannelResponse, error) {
	resp, err := s.channelClient.DeleteChannel(ctx, &channelv1.DeleteChannelRequest{
		ChannelId: req.ChannelId,
	})
	if err != nil {
		return &adminv1.AdminDeleteChannelResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminDeleteChannelResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

func (s *AdminService) ChangeChannelStatus(ctx context.Context, req *adminv1.AdminChangeChannelStatusRequest) (*adminv1.AdminChangeChannelStatusResponse, error) {
	resp, err := s.channelClient.ChangeChannelStatus(ctx, &channelv1.ChangeChannelStatusRequest{
		ChannelId: req.ChannelId,
		Status:    req.Status,
	})
	if err != nil {
		return &adminv1.AdminChangeChannelStatusResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminChangeChannelStatusResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

// ========== 系统配置 ==========

var oneAPIOptionDefaults = map[string]string{
	"PasswordLoginEnabled":           "true",
	"PasswordRegisterEnabled":        "true",
	"EmailVerificationEnabled":       "false",
	"GitHubOAuthEnabled":             "false",
	"OidcEnabled":                    "false",
	"WeChatAuthEnabled":              "false",
	"TurnstileCheckEnabled":          "false",
	"RegisterEnabled":                "true",
	"AutomaticDisableChannelEnabled": "false",
	"AutomaticEnableChannelEnabled":  "false",
	"ApproximateTokenEnabled":        "false",
	"LogConsumeEnabled":              "true",
	"DisplayInCurrencyEnabled":       "false",
	"DisplayTokenStatEnabled":        "true",
	"ChannelDisableThreshold":        "0",
	"EmailDomainRestrictionEnabled":  "false",
	"EmailDomainWhitelist":           "",
	"SMTPServer":                     "",
	"SMTPFrom":                       "",
	"SMTPPort":                       "587",
	"SMTPAccount":                    "",
	"Notice":                         "",
	"About":                          "",
	"HomePageContent":                "",
	"Footer":                         "",
	"SystemName":                     "One-API",
	"Logo":                           "",
	"ServerAddress":                  "",
	"GitHubClientId":                 "",
	"WeChatServerAddress":            "",
	"WeChatAccountQRCodeImageURL":    "",
	"MessagePusherAddress":           "",
	"TurnstileSiteKey":               "",
	"QuotaForNewUser":                "0",
	"QuotaForInviter":                "0",
	"QuotaForInvitee":                "0",
	"QuotaRemindThreshold":           "0",
	"PreConsumedQuota":               "0",
	"ModelRatio":                     "{}",
	"GroupRatio":                     "{}",
	"CompletionRatio":                "{}",
	"TopUpLink":                      "",
	"ChatLink":                       "",
	"QuotaPerUnit":                   "500000",
	"RetryTimes":                     "0",
	"Theme":                          "default",
}

func (s *AdminService) ListOneAPIOptions(context.Context) ([]OneAPIOption, error) {
	values := make(map[string]string, len(oneAPIOptionDefaults))
	for key, value := range oneAPIOptionDefaults {
		values[key] = value
	}
	if s.systemOptsRepo != nil {
		for key := range values {
			if v, err := s.systemOptsRepo.Get(key); err == nil && v != "" {
				values[key] = v
			}
		}
		if v, err := s.systemOptsRepo.Get("site_title"); err == nil && v != "" && values["SystemName"] == oneAPIOptionDefaults["SystemName"] {
			values["SystemName"] = v
		}
		if v, err := s.systemOptsRepo.Get("registration_enabled"); err == nil && v != "" && values["RegisterEnabled"] == oneAPIOptionDefaults["RegisterEnabled"] {
			values["RegisterEnabled"] = v
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.HasSuffix(key, "Token") || strings.HasSuffix(key, "Secret") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	options := make([]OneAPIOption, 0, len(keys))
	for _, key := range keys {
		options = append(options, OneAPIOption{Key: key, Value: values[key]})
	}
	return options, nil
}

func (s *AdminService) UpdateOneAPIOption(_ context.Context, key, value string) (*adminv1.UpdateSystemOptionsResponse, error) {
	if s.systemOptsRepo == nil {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: "system options storage not configured",
		}, nil
	}
	if key == "" {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: "option key is required",
		}, nil
	}
	if err := s.systemOptsRepo.Set(key, value); err != nil {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: fmt.Sprintf("failed to save %s: %v", key, err),
		}, nil
	}
	switch key {
	case "SystemName":
		_ = s.systemOptsRepo.Set("site_title", value)
	case "RegisterEnabled":
		_ = s.systemOptsRepo.Set("registration_enabled", value)
	}
	return &adminv1.UpdateSystemOptionsResponse{
		Success: true,
		Message: "",
	}, nil
}

func (s *AdminService) GetSystemOptions(ctx context.Context, req *adminv1.GetSystemOptionsRequest) (*adminv1.GetSystemOptionsResponse, error) {
	siteTitle := "One-API"
	registrationEnabled := true

	if s.systemOptsRepo != nil {
		if v, err := s.systemOptsRepo.Get("site_title"); err == nil && v != "" {
			siteTitle = v
		}
		if v, err := s.systemOptsRepo.Get("registration_enabled"); err == nil && v != "" {
			registrationEnabled = v == "true"
		}
	}

	return &adminv1.GetSystemOptionsResponse{
		Options: &commonv1.SystemOptions{
			SiteTitle:           siteTitle,
			RegistrationEnabled: registrationEnabled,
		},
	}, nil
}

func (s *AdminService) UpdateSystemOptions(ctx context.Context, req *adminv1.UpdateSystemOptionsRequest) (*adminv1.UpdateSystemOptionsResponse, error) {
	if s.systemOptsRepo == nil {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: "system options storage not configured",
		}, nil
	}

	if req.Options == nil {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: "options is required",
		}, nil
	}

	if err := s.systemOptsRepo.Set("site_title", req.Options.SiteTitle); err != nil {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: fmt.Sprintf("failed to save site_title: %v", err),
		}, nil
	}

	registrationValue := "false"
	if req.Options.RegistrationEnabled {
		registrationValue = "true"
	}
	if err := s.systemOptsRepo.Set("registration_enabled", registrationValue); err != nil {
		return &adminv1.UpdateSystemOptionsResponse{
			Success: false,
			Message: fmt.Sprintf("failed to save registration_enabled: %v", err),
		}, nil
	}

	return &adminv1.UpdateSystemOptionsResponse{
		Success: true,
		Message: "system options updated",
	}, nil
}

// ========== 日志查询 ==========

// ListLogs returns ledger logs by proxying to billing-service.
// Supports filtering by user_id, type, start_time, and end_time.
func (s *AdminService) ListLogs(ctx context.Context, req *adminv1.ListLogsRequest) (*adminv1.ListLogsResponse, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	billingReq := &billingv1.ListLedgerRequest{
		UserId:   req.UserId,
		Page:     page,
		PageSize: pageSize,
	}

	// Pass time range filters to billing service
	if req.StartTime > 0 {
		ts := timestamppb.New(time.Unix(req.StartTime, 0))
		billingReq.StartTime = ts
	}
	if req.EndTime > 0 {
		ts := timestamppb.New(time.Unix(req.EndTime, 0))
		billingReq.EndTime = ts
	}

	billingResp, err := s.billingClient.ListLedger(ctx, billingReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return &adminv1.ListLogsResponse{
				Logs:  []*adminv1.LogEntry{},
				Total: 0,
			}, fmt.Errorf("failed to list ledger: %s", st.Message())
		}
		return &adminv1.ListLogsResponse{
			Logs:  []*adminv1.LogEntry{},
			Total: 0,
		}, fmt.Errorf("failed to list ledger: %w", err)
	}

	logs := make([]*adminv1.LogEntry, 0, len(billingResp.Entries))
	for _, entry := range billingResp.Entries {
		// Filter by type client-side (billing service doesn't support type filter)
		if req.Type != "" && entry.Type != req.Type {
			continue
		}

		var createdAt int64
		if entry.CreatedAt != nil {
			createdAt = entry.CreatedAt.AsTime().Unix()
		}
		logs = append(logs, &adminv1.LogEntry{
			Id:           0, // LedgerEntry.Id is string, LogEntry.Id is int64
			UserId:       entry.UserId,
			Type:         entry.Type,
			Amount:       entry.Amount,
			BalanceAfter: entry.BalanceAfter,
			ReferenceId:  entry.ReferenceId,
			Remark:       entry.Remark,
			CreatedAt:    createdAt,
		})
	}

	return &adminv1.ListLogsResponse{
		Logs:  logs,
		Total: billingResp.Total,
	}, nil
}

func (s *AdminService) GetLogStats(ctx context.Context, req *adminv1.ListLogsRequest) (map[string]interface{}, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 1000
	}
	resp, err := s.ListLogs(ctx, req)
	if err != nil {
		return nil, err
	}
	countByType := map[string]int64{}
	amountByType := map[string]int64{}
	totalAmount := int64(0)
	for _, entry := range resp.Logs {
		countByType[entry.Type]++
		amountByType[entry.Type] += entry.Amount
		totalAmount += entry.Amount
	}
	return map[string]interface{}{
		"total":          resp.Total,
		"sampled_count":  len(resp.Logs),
		"total_amount":   totalAmount,
		"count_by_type":  countByType,
		"amount_by_type": amountByType,
	}, nil
}

// 辅助函数：将 time.Time 转换为 Unix 时间戳
func toUnixTimestamp(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// 辅助函数：将 Unix 时间戳转换为 time.Time
func fromUnixTimestamp(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// 辅助函数：创建错误响应
func errorResponse(message string) error {
	return status.Error(codes.Internal, fmt.Sprintf("internal error: %s", message))
}
