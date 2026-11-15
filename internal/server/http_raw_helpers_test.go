package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"

	"google.golang.org/grpc"
)

type rawIdentityClient struct {
	identityv1.IdentityServiceClient
	userIDByToken map[string]int64
	allowedModels []string
}

func TestHTTPServerApplyModelWhitelistIgnoresCaseAndExtendedContextSuffix(t *testing.T) {
	s := &HTTPServer{}
	got := s.applyModelWhitelist(
		[]string{"deepseek-v4-pro-0813", "gpt-5"},
		[]string{"DeepSeek-V4-Pro-0813[1M]"},
	)
	if len(got) != 1 || got[0] != "deepseek-v4-pro-0813" {
		t.Fatalf("filtered models = %v, want deepseek model", got)
	}
}

func (c rawIdentityClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	userID := int64(42)
	if c.userIDByToken != nil {
		if mapped, ok := c.userIDByToken[req.Token]; ok {
			userID = mapped
		}
	}
	return &identityv1.GetAuthSnapshotReply{
		UserId:        userID,
		TokenId:       7,
		TokenName:     "test-token",
		Group:         "default",
		AllowedModels: c.allowedModels,
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

type rawChannelClient struct {
	channelv1.ChannelServiceClient
	baseURL        string
	key            string
	chType         int32
	apiVersion     string
	models         []string
	getModels      string
	modelMapping   string
	upstreamModel  string
	usageRequests  []*channelv1.RecordChannelUsageRequest
	healthRequests []*channelv1.RecordChannelHealthRequest
	denyRoute      bool
	routeError     error
}

type rawModelHealthClient struct {
	rawChannelClient
	mu       sync.Mutex
	requests []*channelv1.RecordModelHealthRequest
}

func (c *rawModelHealthClient) RecordModelHealth(_ context.Context, req *channelv1.RecordModelHealthRequest, _ ...grpc.CallOption) (*channelv1.RecordModelHealthResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	return &channelv1.RecordModelHealthResponse{Success: true}, nil
}

func (c *rawModelHealthClient) modelHealthRequests() []*channelv1.RecordModelHealthRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*channelv1.RecordModelHealthRequest(nil), c.requests...)
}

func (c rawChannelClient) CheckRoute(_ context.Context, req *channelv1.CheckRouteRequest, _ ...grpc.CallOption) (*channelv1.CheckRouteReply, error) {
	return &channelv1.CheckRouteReply{Allowed: !c.denyRoute && req.Group == "default" && req.Model != "" && req.SourceId > 0 && req.SourceKind == "channel", UpstreamModelId: c.upstreamModel}, c.routeError
}

func (c rawChannelClient) ListAvailableModels(context.Context, *channelv1.ListAvailableModelsRequest, ...grpc.CallOption) (*channelv1.ListAvailableModelsReply, error) {
	return &channelv1.ListAvailableModelsReply{Models: c.models}, nil
}

func (c rawChannelClient) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest, opts ...grpc.CallOption) (*channelv1.SelectChannelReply, error) {
	chType := c.chType
	if chType == 0 {
		chType = 1
	}
	return &channelv1.SelectChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:              11,
			Type:            chType,
			Name:            "openai-compatible",
			Status:          1,
			BaseUrl:         c.baseURL,
			Key:             c.key,
			Group:           req.Group,
			Models:          req.Model,
			UpstreamModelId: c.upstreamModel,
			Config:          &commonv1.ChannelConfig{ApiVersion: c.apiVersion},
		},
	}, nil
}

func (c rawChannelClient) SelectSubscriptionAccount(context.Context, *channelv1.SelectSubscriptionAccountRequest, ...grpc.CallOption) (*channelv1.SelectSubscriptionAccountReply, error) {
	return &channelv1.SelectSubscriptionAccountReply{}, nil
}

func (c rawChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	chType := c.chType
	if chType == 0 {
		chType = 1
	}
	models := c.getModels
	if models == "" {
		models = "gpt-3.5-turbo"
	}
	return &channelv1.GetChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:              req.ChannelId,
			Type:            chType,
			Name:            "openai-compatible",
			Status:          1,
			BaseUrl:         c.baseURL,
			Key:             c.key,
			Group:           "default",
			Models:          models,
			ModelMapping:    c.modelMapping,
			UpstreamModelId: c.upstreamModel,
			Config:          &commonv1.ChannelConfig{ApiVersion: c.apiVersion},
		},
	}, nil
}

func (c rawChannelClient) RecordChannelUsage(ctx context.Context, req *channelv1.RecordChannelUsageRequest, opts ...grpc.CallOption) (*channelv1.RecordChannelUsageResponse, error) {
	c.usageRequests = append(c.usageRequests, req)
	return &channelv1.RecordChannelUsageResponse{Success: true, Message: "ok"}, nil
}

func (c rawChannelClient) RecordChannelHealth(ctx context.Context, req *channelv1.RecordChannelHealthRequest, opts ...grpc.CallOption) (*channelv1.RecordChannelHealthResponse, error) {
	c.healthRequests = append(c.healthRequests, req)
	return &channelv1.RecordChannelHealthResponse{Success: true, Message: "ok"}, nil
}

func (c rawChannelClient) RecordChannelSlot(context.Context, *channelv1.RecordChannelSlotRequest, ...grpc.CallOption) (*channelv1.RecordChannelSlotResponse, error) {
	return &channelv1.RecordChannelSlotResponse{Success: true}, nil
}

func (c rawChannelClient) RecordModelUsage(ctx context.Context, req *channelv1.RecordModelUsageRequest, opts ...grpc.CallOption) (*channelv1.RecordModelUsageResponse, error) {
	return &channelv1.RecordModelUsageResponse{Success: true, Message: "ok"}, nil
}

func (c rawChannelClient) RecordUsageSemanticVerdict(ctx context.Context, req *channelv1.RecordUsageSemanticVerdictRequest, opts ...grpc.CallOption) (*channelv1.RecordUsageSemanticVerdictResponse, error) {
	return &channelv1.RecordUsageSemanticVerdictResponse{}, nil
}

func (c rawChannelClient) ListModelUsageStats(ctx context.Context, req *channelv1.ListModelUsageStatsRequest, opts ...grpc.CallOption) (*channelv1.ListModelUsageStatsResponse, error) {
	return &channelv1.ListModelUsageStatsResponse{Stats: []*channelv1.ModelUsageStat{}, Total: 0}, nil
}

type rawLogClient struct {
	logv1.LogServiceClient
	entries               []*logv1.IngestLogRequest
	failOnCanceledContext bool
}

func (c *rawLogClient) IngestLog(ctx context.Context, req *logv1.IngestLogRequest, opts ...grpc.CallOption) (*logv1.IngestLogResponse, error) {
	if c.failOnCanceledContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.entries = append(c.entries, req)
	return &logv1.IngestLogResponse{Id: int64(len(c.entries))}, nil
}

type rawBillingClient struct {
	tierPricingVersion int32
	billingv1.BillingServiceClient
	commits               int
	commitRequests        []*billingv1.CommitQuotaRequest
	commitErrs            []error
	reserveRequests       []*billingv1.ReserveQuotaRequest
	releases              int
	reserveSuccess        bool
	reserveMessage        string
	commitSuccess         bool
	commitMessage         string
	releaseSuccess        bool
	releaseMessage        string
	failOnCanceledContext bool
	accountSnapshot       *commonv1.AccountSnapshot
	attempts              []*billingv1.RequestAttempt
	listAttemptsCalls     int
	listAttemptsRequests  []*billingv1.ListRequestAttemptsRequest
}

func (c *rawBillingClient) GetRoutingCapabilities(context.Context, *billingv1.GetRoutingCapabilitiesRequest, ...grpc.CallOption) (*billingv1.GetRoutingCapabilitiesResponse, error) {
	return &billingv1.GetRoutingCapabilitiesResponse{ServiceTierPricingVersion: 1}, nil
}

func (c *rawBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	c.reserveRequests = append(c.reserveRequests, req)
	success := c.reserveSuccess
	if !success && c.reserveMessage == "" {
		success = true
	}
	return &billingv1.ReserveQuotaResponse{
		RootRequestId:              req.RootRequestId,
		RequestId:                  req.RequestId,
		AttemptNumber:              req.AttemptNumber,
		SourceKind:                 req.SourceKind,
		UpstreamModelId:            req.UpstreamModelId,
		Success:                    success,
		ErrorMessage:               c.reserveMessage,
		ReservationId:              fmt.Sprintf("reservation-%d", len(c.reserveRequests)),
		ReservedAmount:             req.EstimatedTokens,
		ServiceTierPricingVersion:  c.tierPricingVersion,
		ServiceTierContractVersion: 1,
	}, nil
}

func (c *rawBillingClient) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest, opts ...grpc.CallOption) (*billingv1.CommitQuotaResponse, error) {
	if c.failOnCanceledContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.commits++
	c.commitRequests = append(c.commitRequests, req)
	if len(c.commitErrs) > 0 {
		err := c.commitErrs[0]
		c.commitErrs = c.commitErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	success := c.commitSuccess
	if !success && c.commitMessage == "" {
		success = true
	}
	return &billingv1.CommitQuotaResponse{Success: success, ErrorMessage: c.commitMessage, CommittedAmount: req.ActualTokens}, nil
}

func (c *rawBillingClient) ListRequestAttempts(ctx context.Context, req *billingv1.ListRequestAttemptsRequest, opts ...grpc.CallOption) (*billingv1.ListRequestAttemptsResponse, error) {
	c.listAttemptsCalls++
	c.listAttemptsRequests = append(c.listAttemptsRequests, req)
	// Mirror billing's (page-1)*page_size windowing so recovery pagination is
	// exercised instead of always returning the whole attempt list on page 1.
	size := int(req.GetPageSize())
	if size < 1 {
		size = 100
	}
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	start := (page - 1) * size
	items := []*billingv1.RequestAttempt(nil)
	if start < len(c.attempts) {
		end := start + size
		if end > len(c.attempts) {
			end = len(c.attempts)
		}
		items = c.attempts[start:end]
	}
	return &billingv1.ListRequestAttemptsResponse{Items: items, Total: int64(len(c.attempts))}, nil
}

func (c *rawBillingClient) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReleaseQuotaResponse, error) {
	if c.failOnCanceledContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.releases++
	success := c.releaseSuccess
	if !success && c.releaseMessage == "" {
		success = true
	}
	return &billingv1.ReleaseQuotaResponse{Success: success, ErrorMessage: c.releaseMessage}, nil
}

func (c *rawBillingClient) GetAccountSnapshot(ctx context.Context, req *billingv1.GetAccountSnapshotRequest, opts ...grpc.CallOption) (*billingv1.GetAccountSnapshotResponse, error) {
	if c.accountSnapshot != nil {
		return &billingv1.GetAccountSnapshotResponse{Snapshot: c.accountSnapshot}, nil
	}
	return &billingv1.GetAccountSnapshotResponse{Snapshot: &commonv1.AccountSnapshot{
		UserId:       req.UserId,
		Balance:      1234,
		UsedAmount:   56,
		RequestCount: 7,
		Group:        "default",
		GroupRatio:   1,
		FrozenAmount: 8,
	}}, nil
}

func (c rawIdentityClient) ConsumeTokenQuota(ctx context.Context, req *identityv1.ConsumeTokenQuotaRequest, opts ...grpc.CallOption) (*identityv1.ConsumeTokenQuotaReply, error) {
	return &identityv1.ConsumeTokenQuotaReply{Success: true}, nil
}
