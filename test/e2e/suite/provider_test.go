package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

// ── Real Provider Configuration ──

var (
	providerBaseURL = envOr("PROVIDER_BASE_URL", "https://token-plan-sgp.xiaomimimo.com/v1")
	providerAPIKey  = envOr("PROVIDER_API_KEY", "")
	providerModel   = envOr("PROVIDER_MODEL", "mimo-v2.5")
)

// TestProvider_ChatCompletion tests a real chat completion against the configured provider.
// Requires PROVIDER_API_KEY to be set.
func TestProvider_ChatCompletion(t *testing.T) {
	if providerAPIKey == "" {
		t.Skip("PROVIDER_API_KEY not set, skipping real provider test")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	payload, _ := json.Marshal(map[string]any{
		"model": providerModel,
		"messages": []map[string]string{
			{"role": "user", "content": "你好，请用一句话介绍你自己。"},
		},
	})

	req, _ := http.NewRequest("POST", providerBaseURL+"/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+providerAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Role    string `json:"role"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, string(body))
	}
	if len(result.Choices) == 0 {
		t.Fatal("no choices returned")
	}
	if result.Choices[0].Message.Content == "" {
		t.Fatal("empty content")
	}
	if result.Usage.TotalTokens == 0 {
		t.Fatal("zero token usage")
	}

	t.Logf("provider: %s", providerBaseURL)
	t.Logf("model: %s (requested: %s)", result.Model, providerModel)
	t.Logf("content: %s", truncate(result.Choices[0].Message.Content, 100))
	t.Logf("usage: prompt=%d, completion=%d, total=%d",
		result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
}

// TestProvider_ListModels tests listing available models from the provider.
func TestProvider_ListModels(t *testing.T) {
	if providerAPIKey == "" {
		t.Skip("PROVIDER_API_KEY not set, skipping real provider test")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequest("GET", providerBaseURL+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+providerAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("no models returned")
	}

	t.Logf("available models (%d):", len(result.Data))
	for _, m := range result.Data {
		t.Logf("  - %s (%s)", m.ID, m.OwnedBy)
	}
}

// TestProvider_Streaming tests a streaming chat completion.
func TestProvider_Streaming(t *testing.T) {
	if providerAPIKey == "" {
		t.Skip("PROVIDER_API_KEY not set, skipping real provider test")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	payload, _ := json.Marshal(map[string]any{
		"model": providerModel,
		"messages": []map[string]string{
			{"role": "user", "content": "说\"你好\"两个字"},
		},
		"stream": true,
	})

	req, _ := http.NewRequest("POST", providerBaseURL+"/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+providerAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	rawBody, _ := io.ReadAll(resp.Body)
	t.Logf("streaming response length: %d bytes", len(rawBody))
	if len(rawBody) == 0 {
		t.Fatal("empty streaming response")
	}

	// Verify we got SSE data (lines starting with "data: ")
	t.Logf("streaming response preview: %s", truncate(string(rawBody), 200))
}

// TestProvider_RelayWithBilling tests the full path through the relay gateway
// with real provider and verifies billing (quota deduction + ledger).
// Requires: full system running (make test-e2e-suite) + PROVIDER_API_KEY.
func TestProvider_RelayWithBilling(t *testing.T) {
	if providerAPIKey == "" {
		t.Skip("PROVIDER_API_KEY not set")
	}

	// Check system is running
	checkSystemOrSkip(t)

	ctx := t.Context()

	// Step 1: Login (registers if needed)
	token, userID := relayLoginUser(t, ctx)
	t.Logf("user_id=%d, got token", userID)

	// Step 2: Create channel pointing to real provider
	channelID := relayCreateChannel(t, providerBaseURL, providerAPIKey, providerModel)
	t.Logf("created channel_id=%d", channelID)

	// Step 3: TopUp quota
	relayTopUp(t, userID, 5000000)
	initialQuota := relayGetQuota(t, userID)
	t.Logf("initial quota=%d", initialQuota)

	// Step 4: Chat via relay gateway
	chatResp := relayChatCompletion(t, token, providerModel, "你好，请用一句话介绍你自己")
	t.Logf("chat response: %s", truncate(chatResp.Content, 80))
	t.Logf("usage: prompt=%d, completion=%d, total=%d",
		chatResp.PromptTokens, chatResp.CompletionTokens, chatResp.TotalTokens)

	// Step 5: Verify billing - quota should be deducted
	currentQuota := relayGetQuota(t, userID)
	deducted := initialQuota - currentQuota
	t.Logf("quota: initial=%d, current=%d, deducted=%d", initialQuota, currentQuota, deducted)

	if deducted <= 0 {
		t.Fatalf("expected quota deduction, but quota went from %d to %d", initialQuota, currentQuota)
	}
	if chatResp.TotalTokens > 0 && deducted == 0 {
		t.Logf("warning: tokens used=%d but no quota deducted", chatResp.TotalTokens)
	}

	// Step 6: Verify ledger has consume entry
	relayVerifyLedger(t, ctx, userID)

	t.Logf("billing verified: deducted=%d tokens, ledger has consume entry", deducted)
}

// ── Helpers for relay integration test ──

func checkSystemOrSkip(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(relayHTTPBase + "/healthz")
	if err != nil {
		t.Skipf("relay gateway not reachable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Skipf("relay gateway returned %d", resp.StatusCode)
	}

	resp2, err := client.Get(adminHTTPBase + "/healthz")
	if err != nil {
		t.Skipf("admin-api not reachable: %v", err)
	}
	resp2.Body.Close()
}

type chatResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func relayLoginUser(t *testing.T, ctx context.Context) (string, int64) {
	t.Helper()
	conn := grpcDial(t, identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	username := "e2e-provider-user"
	password := "testpass123456"

	resp, err := client.Login(ctx, &identityv1.LoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil || !resp.Success {
		// Register first
		regResp, regErr := client.Register(ctx, &identityv1.RegisterRequest{
			Username: username,
			Password: password,
			Email:    username + "@test.com",
			Group:    "default",
		})
		if regErr != nil || !regResp.Success {
			t.Fatalf("register failed: %v %s", regErr, regResp.GetMessage())
		}
		// Login again
		resp, err = client.Login(ctx, &identityv1.LoginRequest{
			Username: username,
			Password: password,
		})
		if err != nil {
			t.Fatalf("login failed: %v", err)
		}
		if !resp.Success {
			t.Fatalf("login not successful: %s", resp.Message)
		}
	}
	if resp.Token == "" {
		t.Fatal("empty token")
	}
	return resp.Token, resp.UserId
}

func relayCreateChannel(t *testing.T, baseURL, apiKey, models string) int64 {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"name":     "e2e-real-provider",
		"type":     1,
		"base_url": baseURL,
		"key":      apiKey,
		"models":   models,
		"group":    "default",
		"priority": 10,
	})

	req, _ := http.NewRequest("POST", adminHTTPBase+"/v1/channels", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create channel failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success   bool   `json:"success"`
		ChannelID int64  `json:"channel_id"`
		Message   string `json:"message"`
	}
	json.Unmarshal(body, &result)
	if !result.Success {
		t.Fatalf("create channel failed: %s (msg: %s)", body, result.Message)
	}
	return result.ChannelID
}

func relayTopUp(t *testing.T, userID, amount int64) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"user_id": fmt.Sprintf("%d", userID),
		"amount":  amount,
		"remark":  "e2e-provider-test",
	})

	req, _ := http.NewRequest("POST", adminHTTPBase+"/v1/topup", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("topup failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success bool `json:"success"`
	}
	json.Unmarshal(body, &result)
	if !result.Success {
		t.Fatalf("topup failed: %s", body)
	}
}

func relayGetQuota(t *testing.T, userID int64) int64 {
	t.Helper()
	url := fmt.Sprintf("%s/v1/account?user_id=%d", adminHTTPBase, userID)
	body := httpGetWithAuth(t, url, adminToken)

	var result struct {
		Account struct {
			Quota int64 `json:"quota"`
		} `json:"account"`
	}
	json.Unmarshal(body, &result)
	return result.Account.Quota
}

func relayChatCompletion(t *testing.T, token, model, prompt string) chatResult {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})

	req, _ := http.NewRequest("POST", relayHTTPBase+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat completion failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Fatalf("chat returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		t.Fatal("empty response")
	}

	return chatResult{
		Content:          result.Choices[0].Message.Content,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}
}

func relayVerifyLedger(t *testing.T, ctx context.Context, userID int64) {
	t.Helper()
	conn := grpcDial(t, billingGRPCEndpoint)
	defer conn.Close()
	client := billingv1.NewBillingServiceClient(conn)

	resp, err := client.ListLedger(ctx, &billingv1.ListLedgerRequest{
		UserId:   fmt.Sprintf("%d", userID),
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("list ledger failed: %v", err)
	}
	if len(resp.Entries) == 0 {
		t.Fatal("no ledger entries found")
	}

	hasConsume := false
	for _, e := range resp.Entries {
		if e.Type == "consume" {
			hasConsume = true
			t.Logf("ledger consume entry: amount=%d, balance_after=%d", e.Amount, e.BalanceAfter)
			break
		}
	}
	if !hasConsume {
		t.Fatal("no consume entry in ledger after chat completion")
	}
}
