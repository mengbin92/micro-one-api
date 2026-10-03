package biz

import (
	"context"
	"fmt"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ChannelActionResult struct {
	Success, Skipped                                  bool
	ChannelID                                         int64
	Message, Provider, HealthStatus                   string
	ResponseTime, StatusCode, CheckedAt               int64
	Balance                                           float64
	BalanceUpdatedTime, BalanceRefreshLastSuccessTime int64
	BalanceRefreshLastError                           string
	ConsecutiveBalanceRefreshFailures                 int32
}

type ChannelActionRepo interface {
	SaveChannelAction(context.Context, *Channel, string, *ChannelActionResult, int32, time.Duration) (*Channel, error)
}

// ExecuteChannelAction keeps provider credentials at their owner. Remote IO
// precedes the replayable database transaction; commit rechecks fresh policy,
// locked source facts and the provider configuration used for the request.
func (uc *ChannelUsecase) ExecuteChannelAction(ctx context.Context, id int64, action string) (*ChannelActionResult, error) {
	point, operation := "channel.channels.test", "channel.channel.test"
	if action == "balance_refresh" {
		point, operation = "channel.channels.balance", "channel.channel.balance.refresh"
	} else if action != "test" {
		return nil, fmt.Errorf("unsupported channel action")
	}
	ctx, err := uc.authorizeObject(ctx, point, operation, id, false)
	if err != nil {
		return nil, err
	}
	channel, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &ChannelActionResult{ChannelID: id, Success: true, CheckedAt: time.Now().Unix(), Balance: channel.Balance, BalanceUpdatedTime: channel.BalanceUpdatedTime, BalanceRefreshLastError: channel.BalanceRefreshLastError, BalanceRefreshLastSuccessTime: channel.BalanceRefreshLastSuccessTime, ConsecutiveBalanceRefreshFailures: channel.ConsecutiveBalanceRefreshFailures}
	started := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}
	if action == "balance_refresh" {
		adapter := balanceAdapterForChannel(channel)
		if adapter == nil {
			result.Skipped = true
			result.Message = "balance refresh not supported for this provider"
			return result, nil
		}
		result.Provider = adapter.name
		balance, fetchErr := adapter.fetch(ctx, client, channel)
		if fetchErr != nil {
			result.Success = false
			result.Message = "balance refresh failed"
			result.BalanceRefreshLastError = result.Message
		} else {
			result.Balance = balance
			result.Message = "balance refreshed"
		}
	} else {
		if channel.Type != provider.ChannelTypeAnthropic && !supportsModelsHealthProbe(channel.Type) {
			result.Skipped = true
			result.Message = "active health probe not supported for this provider"
			return result, nil
		}
		upstream, err := provider.NewProviderFactory(30*time.Second).CreateProviderWithConfig(channel.Type, channel.BaseURL, channel.Key, provider.ProviderConfig{APIVersion: channel.Config.APIVersion})
		if err != nil {
			return nil, err
		}
		request := &provider.RawRequest{Method: http.MethodGet, Path: "/models", Header: http.Header{"Accept": []string{"application/json"}}}
		if channel.Type == provider.ChannelTypeAnthropic {
			if len(channel.Models) == 0 {
				return nil, fmt.Errorf("anthropic probe requires a model")
			}
			request.Method, request.Path = http.MethodPost, "/messages"
			request.Header = http.Header{"Content-Type": []string{"application/json"}}
			request.Body, err = jsonx.Marshal(map[string]any{"model": channel.Models[0], "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "ping"}}})
			if err != nil {
				return nil, err
			}
		}
		reply, probeErr := upstream.Forward(ctx, request)
		if probeErr != nil || reply == nil || reply.StatusCode < 200 || reply.StatusCode >= 300 {
			result.Success = false
			result.Message = "channel health probe failed"
		} else {
			result.StatusCode = int64(reply.StatusCode)
			result.Message = "channel health probe succeeded"
		}
	}
	result.ResponseTime = time.Since(started).Milliseconds()
	writer, ok := uc.repo.(ChannelActionRepo)
	if !ok {
		return nil, authorization.ErrDenied
	}
	stored, err := writer.SaveChannelAction(ctx, channel, operation, result, uc.healthFailureThreshold, uc.healthCooldown)
	if err != nil {
		return nil, err
	}
	if uc.selector != nil {
		uc.selector.UpdateChannel(stored)
	}
	result.HealthStatus = stored.EffectiveHealthStatus()
	result.BalanceUpdatedTime = stored.BalanceUpdatedTime
	result.BalanceRefreshLastSuccessTime = stored.BalanceRefreshLastSuccessTime
	result.ConsecutiveBalanceRefreshFailures = stored.ConsecutiveBalanceRefreshFailures
	return result, nil
}

type channelBalanceAdapter struct {
	name  string
	fetch func(context.Context, *http.Client, *Channel) (float64, error)
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

func balanceAdapterForChannel(channel *Channel) *channelBalanceAdapter {
	host := ""
	if parsed, err := url.Parse(channel.BaseURL); err == nil {
		host = strings.ToLower(parsed.Host)
	}
	switch {
	case channel.Type == channelTypeOpenRouter:
		return &channelBalanceAdapter{name: "openrouter_credits", fetch: fetchOpenRouterBalance}
	case channel.Type == channelTypeSiliconFlow:
		return &channelBalanceAdapter{name: "siliconflow_user_info", fetch: fetchSiliconFlowBalance}
	case channel.Type == channelTypeDeepSeek:
		return &channelBalanceAdapter{name: "deepseek_balance", fetch: fetchDeepSeekBalance}
	case strings.Contains(host, "openrouter"):
		return &channelBalanceAdapter{name: "openrouter_credits", fetch: fetchOpenRouterBalance}
	case strings.Contains(host, "siliconflow"):
		return &channelBalanceAdapter{name: "siliconflow_user_info", fetch: fetchSiliconFlowBalance}
	case strings.Contains(host, "deepseek"):
		return &channelBalanceAdapter{name: "deepseek_balance", fetch: fetchDeepSeekBalance}
	case channel.Type == 1:
		return &channelBalanceAdapter{name: "openai_dashboard", fetch: fetchOpenAIDashboardBalance}
	default:
		return nil
	}
}

func fetchOpenAIDashboardBalance(ctx context.Context, client *http.Client, channel *Channel) (float64, error) {
	endpoint := openAIDashboardBalanceEndpoint(channel)
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.Key)
	if err != nil {
		return 0, err
	}
	return firstFloat(payload, "total_available", "total_granted")
}

func openAIDashboardBalanceEndpoint(channel *Channel) string {
	base := strings.TrimRight(channel.BaseURL, "/")
	if base == "" {
		base = openAIDefaultBaseURL
	}
	return strings.TrimRight(trimV1Base(base), "/") + "/dashboard/billing/credit_grants"
}

func fetchOpenRouterBalance(ctx context.Context, client *http.Client, channel *Channel) (float64, error) {
	endpoint := openRouterBalanceEndpoint(channel)
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.Key)
	if err != nil {
		return 0, err
	}
	data, _ := payload["data"].(map[string]any)
	if total, ok := floatFromMap(data, "total_credits"); ok {
		if used, usedOK := floatFromMap(data, "total_usage"); usedOK {
			return total - used, nil
		}
		return total, nil
	}
	return firstFloat(payload, "total_available", "balance")
}

func openRouterBalanceEndpoint(channel *Channel) string {
	base := strings.TrimRight(channel.BaseURL, "/")
	if base == "" {
		base = openRouterDefaultBaseURL
	}
	if strings.HasSuffix(base, "/api/v1") {
		return base + "/credits"
	}
	return strings.TrimRight(trimV1Base(base), "/") + "/api/v1/credits"
}

func fetchSiliconFlowBalance(ctx context.Context, client *http.Client, channel *Channel) (float64, error) {
	endpoint := siliconFlowBalanceEndpoint(channel)
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.Key)
	if err != nil {
		return 0, err
	}
	if data, _ := payload["data"].(map[string]any); data != nil {
		if balance, ok := floatFromMap(data, "balance"); ok {
			return balance, nil
		}
	}
	return firstFloat(payload, "balance", "total_available")
}

func siliconFlowBalanceEndpoint(channel *Channel) string {
	base := strings.TrimRight(channel.BaseURL, "/")
	if base == "" {
		base = siliconFlowDefaultBaseURL
	}
	return base + "/user/info"
}

func fetchDeepSeekBalance(ctx context.Context, client *http.Client, channel *Channel) (float64, error) {
	endpoint := deepSeekBalanceEndpoint(channel)
	payload, err := fetchBalancePayload(ctx, client, endpoint, channel.Key)
	if err != nil {
		return 0, err
	}
	if infos, ok := payload["balance_infos"].([]any); ok {
		total := 0.0
		for _, item := range infos {
			info, _ := item.(map[string]any)
			if balance, ok := floatFromMap(info, "total_balance"); ok {
				total += balance
			}
		}
		return total, nil
	}
	return firstFloat(payload, "balance", "total_available")
}

func deepSeekBalanceEndpoint(channel *Channel) string {
	base := strings.TrimRight(channel.BaseURL, "/")
	if base == "" {
		base = deepSeekDefaultBaseURL
	}
	return strings.TrimRight(trimV1Base(base), "/") + "/user/balance"
}

func fetchBalancePayload(ctx context.Context, client *http.Client, endpoint, key string) (map[string]any, error) {
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
	var payload map[string]any
	if err := jsonx.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func trimV1Base(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	return strings.TrimSuffix(base, "/v1")
}

func firstFloat(payload map[string]any, keys ...string) (float64, error) {
	for _, key := range keys {
		if value, ok := floatFromMap(payload, key); ok {
			return value, nil
		}
	}
	return 0, fmt.Errorf("balance field not found")
}

func floatFromMap(payload map[string]any, key string) (float64, bool) {
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

func supportsModelsHealthProbe(channelType int32) bool {
	switch channelType {
	case provider.ChannelTypeOpenAI,
		provider.ChannelTypeDeepSeek,
		provider.ChannelTypeMistral,
		provider.ChannelTypeMoonshot,
		provider.ChannelTypeGroq,
		provider.ChannelTypeCohere,
		provider.ChannelTypeBaichuan,
		provider.ChannelTypeZhipu,
		provider.ChannelTypeTongyi,
		provider.ChannelTypeMinimax,
		provider.ChannelTypeTogether,
		provider.ChannelTypeFireworks,
		provider.ChannelTypePerplexity,
		provider.ChannelTypeNovita,
		provider.ChannelTypeOpenRouter,
		provider.ChannelTypeSiliconFlow,
		provider.ChannelTypeOllama,
		provider.ChannelTypeDoubao:
		return true
	default:
		return false
	}
}
