package service

import (
	"context"
	"encoding/json"
	"errors"
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
	relayprovider "micro-one-api/internal/relay/provider"
	subscriptionbiz "micro-one-api/internal/subscription/biz"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AdminService is the transport layer entry for admin-api.
type AdminService struct {
	adminv1.UnimplementedAdminServiceServer
	billingClient   billingv1.BillingServiceClient
	identityClient  identityv1.IdentityServiceClient
	channelClient   channelv1.ChannelServiceClient
	systemOptsRepo  SystemOptionsStore
	httpClient      *http.Client
	providerFactory *relayprovider.ProviderFactory
	subscriptionUc  *subscriptionbiz.SubscriptionUsecase
	groupUc         *subscriptionbiz.GroupUsecase
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

type GroupConfig struct {
	Group string  `json:"group"`
	Ratio float64 `json:"ratio"`
}

// NewAdminService creates a new admin service
func NewAdminService(
	billingClient billingv1.BillingServiceClient,
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	systemOptsRepo SystemOptionsStore,
) *AdminService {
	return &AdminService{
		billingClient:   billingClient,
		identityClient:  identityClient,
		channelClient:   channelClient,
		systemOptsRepo:  systemOptsRepo,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		providerFactory: relayprovider.NewProviderFactory(10 * time.Second),
	}
}

// SetSubscriptionUsecases wires optional user-subscription management.
func (s *AdminService) SetSubscriptionUsecases(subscriptionUc *subscriptionbiz.SubscriptionUsecase, groupUc *subscriptionbiz.GroupUsecase) {
	if s == nil {
		return
	}
	s.subscriptionUc = subscriptionUc
	s.groupUc = groupUc
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
		Success:    true,
		NewBalance: resp.NewBalance,
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

func (s *AdminService) ListPaymentOrders(ctx context.Context, req *billingv1.ListPaymentOrdersRequest) (*billingv1.ListPaymentOrdersResponse, error) {
	if s.billingClient == nil {
		return &billingv1.ListPaymentOrdersResponse{}, nil
	}
	return s.billingClient.ListPaymentOrders(ctx, req)
}

func (s *AdminService) GetPaymentOrderByTradeNo(ctx context.Context, req *billingv1.GetPaymentOrderByTradeNoRequest) (*billingv1.PaymentOrderResponse, error) {
	if s.billingClient == nil {
		return &billingv1.PaymentOrderResponse{Success: false, ErrorMessage: "billing service unavailable"}, nil
	}
	return s.billingClient.GetPaymentOrderByTradeNo(ctx, req)
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
			Balance:      resp.Snapshot.Balance,
			UsedAmount:   resp.Snapshot.UsedAmount,
			RequestCount: resp.Snapshot.RequestCount,
			FrozenAmount: resp.Snapshot.FrozenAmount,
			Status:       0,
		},
	}, nil
}

// BatchGetAccountSnapshots 批量获取账户快照
func (s *AdminService) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*commonv1.AccountInfo, error) {
	if s.billingClient == nil || len(userIDs) == 0 {
		return map[string]*commonv1.AccountInfo{}, nil
	}

	resp, err := s.billingClient.BatchGetAccountSnapshots(ctx, &billingv1.BatchGetAccountSnapshotsRequest{
		UserIds: userIDs,
	})
	if err != nil {
		return nil, err
	}

	accounts := make(map[string]*commonv1.AccountInfo, len(resp.GetSnapshots()))
	for userID, snapshot := range resp.GetSnapshots() {
		if snapshot != nil {
			accounts[userID] = &commonv1.AccountInfo{
				UserId:       snapshot.GetUserId(),
				Group:        snapshot.GetGroup(),
				Balance:      snapshot.GetBalance(),
				UsedAmount:   snapshot.GetUsedAmount(),
				RequestCount: snapshot.GetRequestCount(),
				FrozenAmount: snapshot.GetFrozenAmount(),
			}
		}
	}

	return accounts, nil
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
		Balance:     req.Balance,
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

func (s *AdminService) SetUserRole(ctx context.Context, req *adminv1.AdminSetUserRoleRequest) (*adminv1.AdminSetUserRoleResponse, error) {
	resp, err := s.identityClient.SetUserRole(ctx, &identityv1.SetUserRoleRequest{
		UserId:         req.UserId,
		Role:           req.Role,
		OperatorUserId: req.OperatorUserId,
	})
	if err != nil {
		return &adminv1.AdminSetUserRoleResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminSetUserRoleResponse{
		Success: resp.Success,
		Message: resp.Message,
		Role:    resp.Role,
	}, nil
}

// RoleAdmin is the minimum role granting admin-api access. Mirrors
// biz.RoleAdminUser; duplicated here so the admin package does not depend
// on identity's biz layer.
const RoleAdmin int32 = 10

// RoleRoot mirrors biz.RoleRootUser and is reported for system-level
// (shared ADMIN_TOKEN) access.
const RoleRoot int32 = 100

// ErrAdminUnauthorized is returned when a session token is invalid or its
// user lacks the admin role.
var ErrAdminUnauthorized = errors.New("unauthorized")

// AuthorizeAdminToken validates a user session token and returns the owning
// user's id and role. It is used by the admin HTTP guard to authorise
// per-user admin access without the shared ADMIN_TOKEN.
func (s *AdminService) AuthorizeAdminToken(ctx context.Context, token string) (int64, int32, error) {
	if s.identityClient == nil {
		return 0, 0, ErrAdminUnauthorized
	}
	vr, err := s.identityClient.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: token})
	if err != nil {
		return 0, 0, err
	}
	if !vr.GetValid() {
		return 0, 0, ErrAdminUnauthorized
	}
	ur, err := s.identityClient.GetUser(ctx, &identityv1.GetUserRequest{UserId: vr.GetUserId()})
	if err != nil {
		return 0, 0, err
	}
	return vr.GetUserId(), ur.GetUser().GetRole(), nil
}

// ValidateToken validates a user token and returns true if valid.
// This is a simpler version of AuthorizeAdminToken that doesn't check admin role.
func (s *AdminService) ValidateToken(ctx context.Context, token string) (bool, error) {
	if s.identityClient == nil {
		return false, fmt.Errorf("identity service not available")
	}
	vr, err := s.identityClient.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: token})
	if err != nil {
		return false, err
	}
	return vr.GetValid(), nil
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
		currentQuota = snapshot.Snapshot.Balance
	}
	delta := req.NewBalance - currentQuota
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
	startedAt := time.Now()
	resp, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Channel == nil {
		return nil, status.Error(codes.NotFound, "channel not found")
	}
	channel := resp.Channel
	result := map[string]interface{}{
		"success":    true,
		"channel_id": channel.Id,
		"name":       channel.Name,
		"type":       channel.Type,
		"status":     channel.Status,
		"group":      channel.Group,
		"models":     channel.Models,
	}
	if !supportsModelsHealthProbe(channel.GetType()) {
		result["skipped"] = true
		result["message"] = "channel metadata resolved; active probe is not supported for this provider"
		return result, nil
	}
	provider, err := s.providerFactory.CreateProviderWithConfig(channel.GetType(), channel.GetBaseUrl(), channel.GetKey(), relayprovider.ProviderConfig{
		APIVersion: channel.GetConfig().GetApiVersion(),
	})
	if err != nil {
		_ = s.recordChannelHealth(ctx, channelID, false, err.Error(), time.Since(startedAt).Milliseconds())
		return nil, err
	}
	probeResp, err := provider.Forward(ctx, &relayprovider.RawRequest{
		Method: http.MethodGet,
		Path:   "/models",
		Header: http.Header{"Accept": []string{"application/json"}},
	})
	responseTime := time.Since(startedAt).Milliseconds()
	if err != nil {
		_ = s.recordChannelHealth(ctx, channelID, false, err.Error(), responseTime)
		return nil, err
	}
	_ = s.recordChannelHealth(ctx, channelID, true, "", responseTime)
	result["message"] = "channel health probe succeeded"
	result["response_time"] = responseTime
	result["status_code"] = probeResp.StatusCode
	result["health_status"] = "healthy"
	return result, nil
}

func (s *AdminService) recordChannelHealth(ctx context.Context, channelID int64, success bool, message string, responseTime int64) error {
	if s.channelClient == nil || channelID <= 0 {
		return nil
	}
	resp, err := s.channelClient.RecordChannelHealth(ctx, &channelv1.RecordChannelHealthRequest{
		ChannelId:    channelID,
		Success:      success,
		Error:        message,
		ResponseTime: responseTime,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return errors.New(resp.GetMessage())
	}
	return nil
}

func supportsModelsHealthProbe(channelType int32) bool {
	switch channelType {
	case relayprovider.ChannelTypeOpenAI,
		relayprovider.ChannelTypeDeepSeek,
		relayprovider.ChannelTypeMistral,
		relayprovider.ChannelTypeMoonshot,
		relayprovider.ChannelTypeGroq,
		relayprovider.ChannelTypeCohere,
		relayprovider.ChannelTypeBaichuan,
		relayprovider.ChannelTypeZhipu,
		relayprovider.ChannelTypeTongyi,
		relayprovider.ChannelTypeMinimax,
		relayprovider.ChannelTypeTogether,
		relayprovider.ChannelTypeFireworks,
		relayprovider.ChannelTypePerplexity,
		relayprovider.ChannelTypeNovita,
		relayprovider.ChannelTypeOpenRouter,
		relayprovider.ChannelTypeSiliconFlow,
		relayprovider.ChannelTypeOllama,
		relayprovider.ChannelTypeDoubao:
		return true
	default:
		return false
	}
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

func (s *AdminService) ListSubscriptionAccounts(ctx context.Context, req *adminv1.AdminListSubscriptionAccountsRequest) (*adminv1.AdminListSubscriptionAccountsResponse, error) {
	resp, err := s.channelClient.ListSubscriptionAccounts(ctx, &channelv1.ListSubscriptionAccountsRequest{
		Page:     req.Page,
		PageSize: req.PageSize,
		Keyword:  req.Keyword,
		Group:    req.Group,
		Status:   req.Status,
		Platform: req.Platform,
	})
	if err != nil {
		return &adminv1.AdminListSubscriptionAccountsResponse{Accounts: []*commonv1.SubscriptionAccountSummary{}, Total: 0}, nil
	}
	return &adminv1.AdminListSubscriptionAccountsResponse{
		Accounts: resp.Accounts,
		Total:    resp.Total,
	}, nil
}

func (s *AdminService) GetSubscriptionAccount(ctx context.Context, accountID int64) (*commonv1.SubscriptionAccountInfo, error) {
	resp, err := s.channelClient.GetSubscriptionAccount(ctx, &channelv1.GetSubscriptionAccountRequest{AccountId: accountID})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Account == nil {
		return nil, status.Error(codes.NotFound, "subscription account not found")
	}
	return resp.Account, nil
}

func (s *AdminService) CreateSubscriptionAccount(ctx context.Context, req *adminv1.AdminCreateSubscriptionAccountRequest) (*adminv1.AdminCreateSubscriptionAccountResponse, error) {
	resp, err := s.channelClient.CreateSubscriptionAccount(ctx, &channelv1.CreateSubscriptionAccountRequest{
		Name:         req.Name,
		Platform:     req.Platform,
		AccountType:  req.AccountType,
		Group:        req.Group,
		Models:       req.Models,
		Priority:     req.Priority,
		BaseUrl:      req.BaseUrl,
		AccessToken:  req.AccessToken,
		RefreshToken: req.RefreshToken,
		ExpiresAt:    req.ExpiresAt,
		AccountId:    req.AccountId,
		Fingerprint:  req.Fingerprint,
		Metadata:     req.Metadata,
	})
	if err != nil {
		return &adminv1.AdminCreateSubscriptionAccountResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminCreateSubscriptionAccountResponse{
		Success:   resp.Success,
		Message:   resp.Message,
		AccountId: resp.AccountId,
	}, nil
}

func (s *AdminService) UpdateSubscriptionAccount(ctx context.Context, req *adminv1.AdminUpdateSubscriptionAccountRequest) (*adminv1.AdminUpdateSubscriptionAccountResponse, error) {
	resp, err := s.channelClient.UpdateSubscriptionAccount(ctx, &channelv1.UpdateSubscriptionAccountRequest{
		Id:           req.Id,
		Name:         req.Name,
		AccountType:  req.AccountType,
		Group:        req.Group,
		Models:       req.Models,
		Priority:     req.Priority,
		BaseUrl:      req.BaseUrl,
		AccessToken:  req.AccessToken,
		RefreshToken: req.RefreshToken,
		ExpiresAt:    req.ExpiresAt,
		AccountId:    req.AccountId,
		Fingerprint:  req.Fingerprint,
		Metadata:     req.Metadata,
	})
	if err != nil {
		return &adminv1.AdminUpdateSubscriptionAccountResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminUpdateSubscriptionAccountResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

func (s *AdminService) DeleteSubscriptionAccount(ctx context.Context, req *adminv1.AdminDeleteSubscriptionAccountRequest) (*adminv1.AdminDeleteSubscriptionAccountResponse, error) {
	resp, err := s.channelClient.DeleteSubscriptionAccount(ctx, &channelv1.DeleteSubscriptionAccountRequest{AccountId: req.AccountId})
	if err != nil {
		return &adminv1.AdminDeleteSubscriptionAccountResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminDeleteSubscriptionAccountResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

func (s *AdminService) ChangeSubscriptionAccountStatus(ctx context.Context, req *adminv1.AdminChangeSubscriptionAccountStatusRequest) (*adminv1.AdminChangeSubscriptionAccountStatusResponse, error) {
	resp, err := s.channelClient.ChangeSubscriptionAccountStatus(ctx, &channelv1.ChangeSubscriptionAccountStatusRequest{
		AccountId: req.AccountId,
		Status:    req.Status,
	})
	if err != nil {
		return &adminv1.AdminChangeSubscriptionAccountStatusResponse{Success: false, Message: err.Error()}, nil
	}
	return &adminv1.AdminChangeSubscriptionAccountStatusResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

type ChannelBalanceRefreshResult struct {
	Success                           bool    `json:"success"`
	ChannelID                         int64   `json:"channel_id"`
	Provider                          string  `json:"provider,omitempty"`
	Balance                           float64 `json:"balance,omitempty"`
	BalanceUpdatedTime                int64   `json:"balance_updated_time,omitempty"`
	Message                           string  `json:"message,omitempty"`
	Skipped                           bool    `json:"skipped,omitempty"`
	BalanceRefreshLastError           string  `json:"balance_refresh_last_error,omitempty"`
	BalanceRefreshLastSuccessTime     int64   `json:"balance_refresh_last_success_time,omitempty"`
	ConsecutiveBalanceRefreshFailures int32   `json:"consecutive_balance_refresh_failures,omitempty"`
	Disabled                          bool    `json:"disabled,omitempty"`
}

type ReconciliationDiscrepancyView struct {
	Type              string `json:"type,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	ExpectedQuota     int64  `json:"expected_quota,omitempty"`
	ActualQuota       int64  `json:"actual_quota,omitempty"`
	LedgerNetAmount   int64  `json:"ledger_net_amount,omitempty"`
	FrozenQuota       int64  `json:"frozen_quota,omitempty"`
	ChannelID         int64  `json:"channel_id,omitempty"`
	ExpectedUsedQuota int64  `json:"expected_used_quota,omitempty"`
	ActualUsedQuota   int64  `json:"actual_used_quota,omitempty"`
	LedgerQuota       int64  `json:"ledger_quota,omitempty"`
	UpstreamCost      int64  `json:"upstream_cost,omitempty"`
	Difference        int64  `json:"difference,omitempty"`
	LedgerCount       int64  `json:"ledger_count,omitempty"`
	LogCount          int64  `json:"log_count,omitempty"`
	LogQuota          int64  `json:"log_quota,omitempty"`
	CountDiff         int64  `json:"count_diff,omitempty"`
	QuotaDiff         int64  `json:"quota_diff,omitempty"`
}

type ReconciliationRunView struct {
	RunID             int64                           `json:"run_id"`
	RunAt             int64                           `json:"run_at"`
	ExpiredCleaned    int32                           `json:"expired_cleaned"`
	TotalAccounts     int32                           `json:"total_accounts"`
	TotalChannels     int32                           `json:"total_channels"`
	TotalReservations int32                           `json:"total_reservations"`
	DiscrepancyCount  int32                           `json:"discrepancy_count"`
	Discrepancies     []ReconciliationDiscrepancyView `json:"discrepancies,omitempty"`
}

type ListReconciliationRunsResult struct {
	Runs  []*ReconciliationRunView `json:"runs"`
	Total int64                    `json:"total"`
}

type UsageAggregateView struct {
	Key                   string `json:"key"`
	UserID                string `json:"user_id,omitempty"`
	ChannelID             int64  `json:"channel_id,omitempty"`
	SubscriptionAccountID int64  `json:"subscription_account_id,omitempty"`
	Model                 string `json:"model,omitempty"`
	TokenName             string `json:"token_name,omitempty"`
	Type                  string `json:"type,omitempty"`
	Quota                 int64  `json:"quota"`
	UpstreamCost          int64  `json:"upstream_cost"`
	GrossProfit           int64  `json:"gross_profit"`
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	CacheReadTokens       int64  `json:"cache_read_tokens"`
	Count                 int64  `json:"count"`
	ElapsedTime           int64  `json:"elapsed_time"`
}

func (s *AdminService) ListReconciliationRuns(ctx context.Context, page, pageSize int32) (*ListReconciliationRunsResult, error) {
	if s.billingClient == nil {
		return &ListReconciliationRunsResult{Runs: []*ReconciliationRunView{}}, nil
	}
	resp, err := s.billingClient.ListReconciliationRuns(ctx, &billingv1.ListReconciliationRunsRequest{Page: page, PageSize: pageSize})
	if err != nil {
		return nil, err
	}
	out := &ListReconciliationRunsResult{Total: resp.GetTotal(), Runs: make([]*ReconciliationRunView, 0, len(resp.GetRuns()))}
	for _, run := range resp.GetRuns() {
		out.Runs = append(out.Runs, reconciliationRunFromProto(run))
	}
	return out, nil
}

func (s *AdminService) AggregateUsageTopN(ctx context.Context, groupBy string, limit int32) ([]UsageAggregateView, error) {
	if s.billingClient == nil {
		return []UsageAggregateView{}, nil
	}
	if limit <= 0 {
		limit = 5
	}
	resp, err := s.billingClient.AggregateUsage(ctx, &billingv1.AggregateUsageRequest{
		GroupBy: []string{groupBy},
		Type:    "consume",
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]UsageAggregateView, 0, len(resp.GetBuckets()))
	for _, bucket := range resp.GetBuckets() {
		items = append(items, usageAggregateViewFromBucket(bucket, groupBy))
	}
	return items, nil
}

func usageAggregateViewFromBucket(bucket *billingv1.UsageBucket, groupBy string) UsageAggregateView {
	view := UsageAggregateView{
		UserID:                bucket.GetUserId(),
		ChannelID:             bucket.GetChannelId(),
		SubscriptionAccountID: bucket.GetSubscriptionAccountId(),
		Model:                 bucket.GetModel(),
		TokenName:             bucket.GetTokenName(),
		Type:                  bucket.GetType(),
		Quota:                 bucket.GetQuota(),
		UpstreamCost:          bucket.GetUpstreamCost(),
		GrossProfit:           bucket.GetGrossProfit(),
		PromptTokens:          bucket.GetPromptTokens(),
		CompletionTokens:      bucket.GetCompletionTokens(),
		CacheReadTokens:       bucket.GetCacheReadTokens(),
		Count:                 bucket.GetCount(),
		ElapsedTime:           bucket.GetElapsedTime(),
	}
	switch groupBy {
	case "user":
		view.Key = bucket.GetUserId()
	case "channel":
		view.Key = strconv.FormatInt(bucket.GetChannelId(), 10)
	case "subscription_account":
		view.Key = strconv.FormatInt(bucket.GetSubscriptionAccountId(), 10)
	case "model":
		view.Key = bucket.GetModel()
	case "token":
		view.Key = bucket.GetTokenName()
	case "type":
		view.Key = bucket.GetType()
	default:
		view.Key = bucket.GetModel()
		if view.Key == "" {
			view.Key = view.UserID
		}
	}
	return view
}

func (s *AdminService) GetReconciliationRun(ctx context.Context, runID int64) (*ReconciliationRunView, error) {
	if s.billingClient == nil {
		return nil, nil
	}
	resp, err := s.billingClient.GetReconciliationRun(ctx, &billingv1.GetReconciliationRunRequest{RunId: runID})
	if err != nil {
		return nil, err
	}
	return reconciliationRunFromProto(resp.GetRun()), nil
}

func reconciliationRunFromProto(run *billingv1.ReconciliationRun) *ReconciliationRunView {
	if run == nil {
		return nil
	}
	view := &ReconciliationRunView{
		RunID:             run.GetRunId(),
		RunAt:             run.GetRunAt(),
		ExpiredCleaned:    run.GetExpiredCleaned(),
		TotalAccounts:     run.GetTotalAccounts(),
		TotalChannels:     run.GetTotalChannels(),
		TotalReservations: run.GetTotalReservations(),
		DiscrepancyCount:  run.GetDiscrepancyCount(),
	}
	for _, d := range run.GetDiscrepancies() {
		view.Discrepancies = append(view.Discrepancies, ReconciliationDiscrepancyView{
			Type:              d.GetType(),
			UserID:            d.GetUserId(),
			ExpectedQuota:     d.GetExpectedQuota(),
			ActualQuota:       d.GetActualQuota(),
			LedgerNetAmount:   d.GetLedgerNetAmount(),
			FrozenQuota:       d.GetFrozenQuota(),
			ChannelID:         d.GetChannelId(),
			ExpectedUsedQuota: d.GetExpectedUsedQuota(),
			ActualUsedQuota:   d.GetActualUsedQuota(),
			LedgerQuota:       d.GetLedgerQuota(),
			UpstreamCost:      d.GetUpstreamCost(),
			Difference:        d.GetDifference(),
			LedgerCount:       d.GetLedgerCount(),
			LogCount:          d.GetLogCount(),
			LogQuota:          d.GetLogQuota(),
			CountDiff:         d.GetCountDiff(),
			QuotaDiff:         d.GetQuotaDiff(),
		})
	}
	return view
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
		// Unsupported provider: keep the channel enabled, do not persist anything,
		// and do not touch the failure counter. Upstream one-api has no balance fetch
		// for these providers, so "stale balance + enabled" is the parity stance.
		return &ChannelBalanceRefreshResult{
			Success:                           true,
			Skipped:                           true,
			ChannelID:                         channel.GetId(),
			Balance:                           channel.GetBalance(),
			BalanceUpdatedTime:                channel.GetBalanceUpdatedTime(),
			BalanceRefreshLastError:           channel.GetBalanceRefreshLastError(),
			BalanceRefreshLastSuccessTime:     channel.GetBalanceRefreshLastSuccessTime(),
			ConsecutiveBalanceRefreshFailures: channel.GetConsecutiveBalanceRefreshFailures(),
			Message:                           "balance refresh not supported for this provider; channel left enabled with stale balance",
		}, nil
	}
	balance, fetchErr := adapter.fetch(ctx, s.httpClient, channel)
	if fetchErr != nil {
		return s.persistBalanceRefreshFailure(ctx, channel, adapter.name, fetchErr)
	}
	return s.persistBalanceRefreshSuccess(ctx, channel, adapter.name, balance)
}

func (s *AdminService) persistBalanceRefreshSuccess(ctx context.Context, channel *commonv1.ChannelInfo, provider string, balance float64) (*ChannelBalanceRefreshResult, error) {
	now := time.Now().Unix()
	resp, err := s.channelClient.UpdateChannel(ctx, &channelv1.UpdateChannelRequest{
		ChannelId:                         channel.GetId(),
		Balance:                           balance,
		BalanceUpdatedTime:                now,
		BalanceRefreshLastError:           "",
		BalanceRefreshLastSuccessTime:     now,
		ConsecutiveBalanceRefreshFailures: 0,
		SetBalanceRefreshFields:           true,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetSuccess() {
		message := resp.GetMessage()
		if message == "" {
			message = "failed to persist channel balance"
		}
		return &ChannelBalanceRefreshResult{Success: false, ChannelID: channel.GetId(), Provider: provider, Message: message}, nil
	}
	return &ChannelBalanceRefreshResult{
		Success:                       true,
		ChannelID:                     channel.GetId(),
		Provider:                      provider,
		Balance:                       balance,
		BalanceUpdatedTime:            now,
		BalanceRefreshLastSuccessTime: now,
	}, nil
}

func (s *AdminService) persistBalanceRefreshFailure(ctx context.Context, channel *commonv1.ChannelInfo, provider string, fetchErr error) (*ChannelBalanceRefreshResult, error) {
	newFailureCount := channel.GetConsecutiveBalanceRefreshFailures() + 1
	resp, err := s.channelClient.UpdateChannel(ctx, &channelv1.UpdateChannelRequest{
		ChannelId:                         channel.GetId(),
		BalanceRefreshLastError:           fetchErr.Error(),
		BalanceRefreshLastSuccessTime:     channel.GetBalanceRefreshLastSuccessTime(),
		ConsecutiveBalanceRefreshFailures: newFailureCount,
		SetBalanceRefreshFields:           true,
	})
	if err != nil {
		return nil, err
	}
	result := &ChannelBalanceRefreshResult{
		Success:                           false,
		ChannelID:                         channel.GetId(),
		Provider:                          provider,
		Message:                           fetchErr.Error(),
		BalanceRefreshLastError:           fetchErr.Error(),
		BalanceRefreshLastSuccessTime:     channel.GetBalanceRefreshLastSuccessTime(),
		ConsecutiveBalanceRefreshFailures: newFailureCount,
	}
	if !resp.GetSuccess() {
		// Persist failed; still report the fetch error to the caller but flag persist failure too.
		return result, nil
	}
	if channel.GetStatus() == 1 && s.shouldAutoDisableChannel(ctx, newFailureCount) {
		disableResp, disableErr := s.channelClient.ChangeChannelStatus(ctx, &channelv1.ChangeChannelStatusRequest{
			ChannelId: channel.GetId(),
			Status:    2,
		})
		if disableErr == nil && disableResp.GetSuccess() {
			result.Disabled = true
		}
	}
	return result, nil
}

// shouldAutoDisableChannel reads the AutomaticDisableChannelEnabled flag and ChannelDisableThreshold
// from system options at refresh time. Returns true only when both are configured AND the threshold
// has been reached. With the default options (false / 0) this always returns false, so existing
// deployments see no behavior change until they opt in.
func (s *AdminService) shouldAutoDisableChannel(ctx context.Context, failureCount int32) bool {
	enabled, err := s.GetOneAPIOption(ctx, "AutomaticDisableChannelEnabled")
	if err != nil || !strings.EqualFold(strings.TrimSpace(enabled), "true") {
		return false
	}
	thresholdStr, err := s.GetOneAPIOption(ctx, "ChannelDisableThreshold")
	if err != nil {
		return false
	}
	threshold, err := strconv.ParseInt(strings.TrimSpace(thresholdStr), 10, 32)
	if err != nil || threshold <= 0 {
		return false
	}
	return int64(failureCount) >= threshold
}

type channelBalanceAdapter struct {
	name  string
	fetch func(context.Context, *http.Client, *commonv1.ChannelInfo) (float64, error)
}

const (
	channelTypeOpenAI      int32 = 1
	channelTypeDeepSeek    int32 = 6
	channelTypeOpenRouter  int32 = 23
	channelTypeSiliconFlow int32 = 24

	openAIDefaultBaseURL      = "https://api.openai.com/v1"
	deepSeekDefaultBaseURL    = "https://api.deepseek.com/v1"
	openRouterDefaultBaseURL  = "https://openrouter.ai/api/v1"
	siliconFlowDefaultBaseURL = "https://api.siliconflow.cn/v1"
)

func balanceAdapterForChannel(channel *commonv1.ChannelInfo) *channelBalanceAdapter {
	host := ""
	if parsed, err := url.Parse(channel.GetBaseUrl()); err == nil {
		host = strings.ToLower(parsed.Host)
	}
	switch {
	case channel.GetType() == channelTypeOpenRouter:
		return &channelBalanceAdapter{name: "openrouter_credits", fetch: fetchOpenRouterBalance}
	case channel.GetType() == channelTypeSiliconFlow:
		return &channelBalanceAdapter{name: "siliconflow_user_info", fetch: fetchSiliconFlowBalance}
	case channel.GetType() == channelTypeDeepSeek:
		return &channelBalanceAdapter{name: "deepseek_balance", fetch: fetchDeepSeekBalance}
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
	endpoint := openAIDashboardBalanceEndpoint(channel)
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.GetKey())
	if err != nil {
		return 0, err
	}
	return firstFloat(payload, "total_available", "total_granted")
}

func openAIDashboardBalanceEndpoint(channel *commonv1.ChannelInfo) string {
	base := strings.TrimRight(channel.GetBaseUrl(), "/")
	if base == "" {
		base = openAIDefaultBaseURL
	}
	return strings.TrimRight(trimV1Base(base), "/") + "/dashboard/billing/credit_grants"
}

func fetchOpenRouterBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := openRouterBalanceEndpoint(channel)
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

func openRouterBalanceEndpoint(channel *commonv1.ChannelInfo) string {
	base := strings.TrimRight(channel.GetBaseUrl(), "/")
	if base == "" {
		base = openRouterDefaultBaseURL
	}
	if strings.HasSuffix(base, "/api/v1") {
		return base + "/credits"
	}
	return strings.TrimRight(trimV1Base(base), "/") + "/api/v1/credits"
}

func fetchSiliconFlowBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := siliconFlowBalanceEndpoint(channel)
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

func siliconFlowBalanceEndpoint(channel *commonv1.ChannelInfo) string {
	base := strings.TrimRight(channel.GetBaseUrl(), "/")
	if base == "" {
		base = siliconFlowDefaultBaseURL
	}
	return base + "/user/info"
}

func fetchDeepSeekBalance(ctx context.Context, client *http.Client, channel *commonv1.ChannelInfo) (float64, error) {
	endpoint := deepSeekBalanceEndpoint(channel)
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

func deepSeekBalanceEndpoint(channel *commonv1.ChannelInfo) string {
	base := strings.TrimRight(channel.GetBaseUrl(), "/")
	if base == "" {
		base = deepSeekDefaultBaseURL
	}
	return strings.TrimRight(trimV1Base(base), "/") + "/user/balance"
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
	"AmountForNewUser":               "0",
	"AmountForInviter":               "0",
	"AmountForInvitee":               "0",
	"QuotaRemindThreshold":           "0",
	"PreConsumedQuota":               "0",
	"ModelRatio":                     "{}",
	"ModelPrice":                     "{}",
	"GroupRatio":                     "{}",
	"CompletionRatio":                "{}",
	"TopUpLink":                      "",
	"ChatLink":                       "",
	"AmountPerUnit":                  "10000",
	"RetryTimes":                     "0",
	"Theme":                          "default",
}

var oneAPIOptionLegacyAliases = map[string]string{
	"AmountForNewUser": "QuotaForNewUser",
	"AmountForInviter": "QuotaForInviter",
	"AmountForInvitee": "QuotaForInvitee",
	"AmountPerUnit":    "QuotaPerUnit",
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
				continue
			}
			if legacyKey := oneAPIOptionLegacyAliases[key]; legacyKey != "" {
				if v, err := s.systemOptsRepo.Get(legacyKey); err == nil && v != "" {
					values[key] = v
				}
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

func (s *AdminService) GetOneAPIOption(_ context.Context, key string) (string, error) {
	if s.systemOptsRepo != nil {
		if v, err := s.systemOptsRepo.Get(key); err == nil && v != "" {
			return v, nil
		}
		if legacyKey := oneAPIOptionLegacyAliases[key]; legacyKey != "" {
			if v, err := s.systemOptsRepo.Get(legacyKey); err == nil && v != "" {
				return v, nil
			}
		}
	}
	return oneAPIOptionDefaults[key], nil
}

func (s *AdminService) ListGroups(ctx context.Context) ([]GroupConfig, error) {
	ratios, err := s.groupRatios(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := ratios["default"]; !ok {
		ratios["default"] = 1
	}
	keys := make([]string, 0, len(ratios))
	for group := range ratios {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	result := make([]GroupConfig, 0, len(keys))
	for _, group := range keys {
		result = append(result, GroupConfig{Group: group, Ratio: ratios[group]})
	}
	return result, nil
}

func (s *AdminService) UpsertGroup(ctx context.Context, group string, ratio float64) (*GroupConfig, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, fmt.Errorf("group is required")
	}
	if ratio <= 0 {
		return nil, fmt.Errorf("ratio must be greater than 0")
	}
	ratios, err := s.groupRatios(ctx)
	if err != nil {
		return nil, err
	}
	ratios[group] = ratio
	if err := s.saveGroupRatios(ratios); err != nil {
		return nil, err
	}
	return &GroupConfig{Group: group, Ratio: ratio}, nil
}

func (s *AdminService) DeleteGroup(ctx context.Context, group string) (*GroupConfig, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, fmt.Errorf("group is required")
	}
	if group == "default" {
		return nil, fmt.Errorf("default group cannot be deleted")
	}
	ratios, err := s.groupRatios(ctx)
	if err != nil {
		return nil, err
	}
	ratio := ratios[group]
	delete(ratios, group)
	if _, ok := ratios["default"]; !ok {
		ratios["default"] = 1
	}
	if err := s.saveGroupRatios(ratios); err != nil {
		return nil, err
	}
	return &GroupConfig{Group: group, Ratio: ratio}, nil
}

func (s *AdminService) groupRatios(ctx context.Context) (map[string]float64, error) {
	raw, err := s.GetOneAPIOption(ctx, "GroupRatio")
	if err != nil {
		return nil, err
	}
	ratios := map[string]float64{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &ratios); err != nil {
			return nil, fmt.Errorf("invalid GroupRatio option: %w", err)
		}
	}
	return ratios, nil
}

func (s *AdminService) saveGroupRatios(ratios map[string]float64) error {
	if s.systemOptsRepo == nil {
		return fmt.Errorf("system options storage not configured")
	}
	keys := make([]string, 0, len(ratios))
	for group := range ratios {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	ordered := make(map[string]float64, len(keys))
	for _, group := range keys {
		ordered[group] = ratios[group]
	}
	payload, err := json.Marshal(ordered)
	if err != nil {
		return err
	}
	return s.systemOptsRepo.Set("GroupRatio", string(payload))
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

	// Build billing request with all filters
	billingReq := &billingv1.ListLedgerRequest{
		UserId:   req.UserId,
		Page:     page,
		PageSize: pageSize,
		Type:     req.Type,
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

	// Billing service now supports type filtering server-side
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
		var createdAt int64
		if entry.CreatedAt != nil {
			createdAt = entry.CreatedAt.AsTime().Unix()
		}
		logs = append(logs, &adminv1.LogEntry{
			Id:           parseInt64(entry.Id),
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

// GetLogStats returns ledger statistics computed by real SQL aggregation in the
// billing service (GROUP BY type), rather than summing a sampled page of rows.
// total_amount is the true consumed quota (ABS sum of consume entries).
func (s *AdminService) GetLogStats(ctx context.Context, req *adminv1.ListLogsRequest) (map[string]interface{}, error) {
	aggReq := &billingv1.AggregateUsageRequest{
		GroupBy: []string{"type"},
		UserId:  req.UserId,
	}
	if req.StartTime > 0 {
		aggReq.StartTime = timestamppb.New(time.Unix(req.StartTime, 0))
	}
	if req.EndTime > 0 {
		aggReq.EndTime = timestamppb.New(time.Unix(req.EndTime, 0))
	}

	resp, err := s.billingClient.AggregateUsage(ctx, aggReq)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			return nil, fmt.Errorf("failed to aggregate usage: %s", st.Message())
		}
		return nil, fmt.Errorf("failed to aggregate usage: %w", err)
	}

	countByType := map[string]int64{}
	amountByType := map[string]int64{}
	upstreamCostByType := map[string]int64{}
	grossProfitByType := map[string]int64{}
	totalCount := int64(0)
	upstreamCost := int64(0)
	grossProfit := int64(0)
	for _, bucket := range resp.GetBuckets() {
		countByType[bucket.GetType()] = bucket.GetCount()
		amountByType[bucket.GetType()] = bucket.GetQuota()
		upstreamCostByType[bucket.GetType()] = bucket.GetUpstreamCost()
		grossProfitByType[bucket.GetType()] = bucket.GetGrossProfit()
		totalCount += bucket.GetCount()
	}
	upstreamCost = upstreamCostByType["consume"]
	grossProfit = grossProfitByType["consume"]

	return map[string]interface{}{
		"total":                 totalCount,
		"total_amount":          amountByType["consume"],
		"upstream_cost":         upstreamCost,
		"gross_profit":          grossProfit,
		"count_by_type":         countByType,
		"amount_by_type":        amountByType,
		"upstream_cost_by_type": upstreamCostByType,
		"gross_profit_by_type":  grossProfitByType,
	}, nil
}

// ListLedgerEntries returns raw billing ledger entries with all fields preserved.
// This is used for admin API responses that need full entry data.
func (s *AdminService) ListLedgerEntries(ctx context.Context, req *adminv1.ListLogsRequest) ([]map[string]interface{}, int64, error) {
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
		Type:     req.Type,
	}

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
		return nil, 0, err
	}

	entries := make([]map[string]interface{}, 0, len(billingResp.GetEntries()))
	for _, entry := range billingResp.GetEntries() {
		var createdAt int64
		if entry.GetCreatedAt() != nil {
			createdAt = entry.GetCreatedAt().AsTime().Unix()
		}
		entries = append(entries, map[string]interface{}{
			"id":                    parseInt64(entry.GetId()),
			"userId":                entry.GetUserId(),
			"type":                  entry.GetType(),
			"amount":                entry.GetAmount(),
			"balanceAfter":          entry.GetBalanceAfter(),
			"referenceId":           entry.GetReferenceId(),
			"remark":                entry.GetRemark(),
			"createdAt":             createdAt,
			"tokenName":             entry.GetTokenName(),
			"modelName":             entry.GetModelName(),
			"quota":                 entry.GetQuota(),
			"promptTokens":          entry.GetPromptTokens(),
			"completionTokens":      entry.GetCompletionTokens(),
			"cacheReadTokens":       entry.GetCacheReadTokens(),
			"channelId":             entry.GetChannelId(),
			"subscriptionAccountId": entry.GetSubscriptionAccountId(),
			"elapsedTime":           entry.GetElapsedTime(),
			"isStream":              entry.GetIsStream(),
			"endpoint":              entry.GetEndpoint(),
		})
	}

	return entries, billingResp.GetTotal(), nil
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

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
