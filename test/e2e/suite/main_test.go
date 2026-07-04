package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

// ── Configuration ──

var (
	identityGRPCEndpoint = envOr("IDENTITY_GRPC_ENDPOINT", "127.0.0.1:9001")
	billingGRPCEndpoint  = envOr("BILLING_GRPC_ENDPOINT", "127.0.0.1:9004")
	relayHTTPBase        = envOr("RELAY_HTTP_BASE", "http://127.0.0.1:8080")
	adminHTTPBase        = envOr("ADMIN_HTTP_BASE", "http://127.0.0.1:3000")
	adminToken           = envOr("ADMIN_TOKEN", "test-admin-token-for-dev")

	testUsername = "e2e-test-user"
	testPassword = "testpass123456"
	testEmail    = "e2e@test.com"
	testModel    = envOr("TEST_MODEL", "gpt-3.5-turbo")

	// Real provider config (set PROVIDER_API_KEY to enable)
	useRealProvider = os.Getenv("PROVIDER_API_KEY") != ""
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Shared state across subtests ──

type e2eState struct {
	sessionToken string
	apiToken     string
	userID       int64
}

// ── Test Suite ──

func TestE2E_FullFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	state := &e2eState{}

	// Phase 1: Identity
	t.Run("Identity/Register", func(t *testing.T) { stepRegister(t, ctx, state) })
	t.Run("Identity/Login", func(t *testing.T) { stepLogin(t, ctx, state) })
	t.Run("Identity/CreateAPIToken", func(t *testing.T) { stepCreateAPIToken(t, state) })
	t.Run("Identity/ValidateToken", func(t *testing.T) { stepValidateToken(t, ctx, state) })
	t.Run("Identity/GetAuthSnapshot", func(t *testing.T) { stepGetAuthSnapshot(t, ctx, state) })

	// Phase 1.5: Setup - topup and optionally create real provider channel
	t.Run("Admin/InitialTopUp", func(t *testing.T) { stepAdminTopUp(t, state) })
	if useRealProvider {
		t.Run("Admin/CreateProviderChannel", func(t *testing.T) { stepAdminCreateProviderChannel(t) })
	}

	// Phase 2: Relay Gateway
	t.Run("Relay/Health", func(t *testing.T) { stepRelayHealth(t) })
	t.Run("Relay/ListModels", func(t *testing.T) { stepListModels(t, state) })
	t.Run("Relay/ChatCompletion", func(t *testing.T) { stepChatCompletion(t, state) })

	// Phase 3: Billing
	t.Run("Billing/AccountSnapshot", func(t *testing.T) { stepAccountSnapshot(t, ctx, state) })
	t.Run("Billing/BalanceDeducted", func(t *testing.T) { stepVerifyBalanceDeducted(t, ctx, state) })
	t.Run("Billing/Ledger", func(t *testing.T) { stepVerifyLedger(t, ctx, state) })

	// Phase 4: Admin - User Management
	t.Run("Admin/Access", func(t *testing.T) { stepAdminAccess(t) })
	t.Run("Admin/ListUsers", func(t *testing.T) { stepAdminListUsers(t) })
	t.Run("Admin/GetUser", func(t *testing.T) { stepAdminGetUser(t, state) })
	t.Run("Admin/UpdateUser", func(t *testing.T) { stepAdminUpdateUser(t, state) })

	// Phase 5: Admin - Channel Management
	t.Run("Admin/ListChannels", func(t *testing.T) { stepAdminListChannels(t) })

	// Phase 6: Admin - Redeem Codes
	t.Run("Admin/CreateRedeemCode", func(t *testing.T) { stepAdminCreateRedeemCode(t) })
	t.Run("Admin/ListRedeemCodes", func(t *testing.T) { stepAdminListRedeemCodes(t) })
	t.Run("Admin/GetRedeemCode", func(t *testing.T) { stepAdminGetRedeemCode(t) })
	t.Run("Admin/DeleteRedeemCode", func(t *testing.T) { stepAdminDeleteRedeemCode(t) })

	// Phase 7: Admin - System
	t.Run("Admin/SystemOptions", func(t *testing.T) { stepAdminSystemOptions(t) })
	t.Run("Admin/Logs", func(t *testing.T) { stepAdminLogs(t, state) })

	// Phase 8: Admin - TopUp (additional)
	t.Run("Admin/SecondTopUp", func(t *testing.T) { stepAdminTopUp(t, state) })
}

// ═══════════════════════════════════════════════════
// Phase 1: Identity Service
// ═══════════════════════════════════════════════════

func stepRegister(t *testing.T, ctx context.Context, state *e2eState) {
	conn := grpcDial(t, identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.Register(ctx, &identityv1.RegisterRequest{
		Username: testUsername,
		Password: testPassword,
		Email:    testEmail,
		Group:    "default",
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("register not successful: %s", resp.Message)
	}
	if resp.UserId == 0 {
		t.Fatal("register returned user_id=0")
	}
	state.userID = resp.UserId
	t.Logf("registered user_id=%d", resp.UserId)
}

func stepLogin(t *testing.T, ctx context.Context, state *e2eState) {
	conn := grpcDial(t, identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.Login(ctx, &identityv1.LoginRequest{
		Username: testUsername,
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("login not successful: %s", resp.Message)
	}
	if resp.Token == "" {
		t.Fatal("login returned empty token")
	}
	state.sessionToken = resp.Token
	if state.userID == 0 {
		state.userID = resp.UserId
	}
	t.Logf("login token=%s...", resp.Token[:min(8, len(resp.Token))])
}

func stepCreateAPIToken(t *testing.T, state *e2eState) {
	payload, _ := json.Marshal(map[string]interface{}{
		"name":            "e2e-api-token",
		"models":          []string{testModel},
		"unlimited_quota": true,
	})

	req, _ := http.NewRequest("POST", adminHTTPBase+"/api/token", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+state.sessionToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create API token failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create API token returned %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success || result.Data.Key == "" {
		t.Fatalf("create API token not successful: %s", body)
	}
	state.apiToken = result.Data.Key
	t.Logf("created API token=%s...", state.apiToken[:min(8, len(state.apiToken))])
}

func stepValidateToken(t *testing.T, ctx context.Context, state *e2eState) {
	conn := grpcDial(t, identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: state.sessionToken})
	if err != nil {
		t.Fatalf("validate token failed: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("token should be valid: %s", resp.Message)
	}
	if resp.UserId != state.userID {
		t.Fatalf("user_id mismatch: expected %d, got %d", state.userID, resp.UserId)
	}
	t.Logf("token validated: user_id=%d, token_id=%d", resp.UserId, resp.TokenId)
}

func stepGetAuthSnapshot(t *testing.T, ctx context.Context, state *e2eState) {
	conn := grpcDial(t, identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{Token: state.apiToken})
	if err != nil {
		t.Fatalf("get auth snapshot failed: %v", err)
	}
	if resp.UserId != state.userID {
		t.Fatalf("user_id mismatch: expected %d, got %d", state.userID, resp.UserId)
	}
	if !resp.UserEnabled {
		t.Fatal("user should be enabled")
	}
	t.Logf("auth snapshot: group=%s, models=%v", resp.Group, resp.AllowedModels)
}

// ═══════════════════════════════════════════════════
// Phase 2: Relay Gateway
// ═══════════════════════════════════════════════════

func stepRelayHealth(t *testing.T) {
	resp, err := http.Get(relayHTTPBase + "/healthz")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health check returned %d", resp.StatusCode)
	}
}

func stepListModels(t *testing.T, state *e2eState) {
	req, _ := http.NewRequest("GET", relayHTTPBase+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+state.apiToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list models failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list models returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected at least one model")
	}

	found := false
	for _, m := range result.Data {
		if m.ID == testModel {
			found = true
			break
		}
	}
	if !found {
		t.Logf("models: %v", result.Data)
		t.Fatalf("model %s not found in list", testModel)
	}
	t.Logf("listed %d models, includes %s", len(result.Data), testModel)
}

func stepChatCompletion(t *testing.T, state *e2eState) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":    testModel,
		"messages": []map[string]string{{"role": "user", "content": "Hello, how are you?"}},
	})

	req, _ := http.NewRequest("POST", relayHTTPBase+"/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+state.apiToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat completion failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat completion returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		t.Fatal("empty response content")
	}
	t.Logf("chat completion: tokens=%d, content=%q", result.Usage.TotalTokens, truncate(result.Choices[0].Message.Content, 60))
}

// ═══════════════════════════════════════════════════
// Phase 3: Billing
// ═══════════════════════════════════════════════════

func stepAccountSnapshot(t *testing.T, ctx context.Context, state *e2eState) {
	conn := grpcDial(t, billingGRPCEndpoint)
	defer conn.Close()
	client := billingv1.NewBillingServiceClient(conn)

	resp, err := client.GetAccountSnapshot(ctx, &billingv1.GetAccountSnapshotRequest{
		UserId: fmt.Sprintf("%d", state.userID),
	})
	if err != nil {
		t.Fatalf("get account snapshot failed: %v", err)
	}
	if resp.Snapshot == nil {
		t.Fatal("nil snapshot")
	}
	t.Logf("account snapshot: balance=%d", resp.Snapshot.Balance)
}

func stepVerifyBalanceDeducted(t *testing.T, ctx context.Context, state *e2eState) {
	url := fmt.Sprintf("%s/v1/account?user_id=%d", adminHTTPBase, state.userID)
	body := httpGetWithAuth(t, url, adminToken)

	var result struct {
		Account struct {
			Balance int64 `json:"balance"`
		} `json:"account"`
	}
	json.Unmarshal(body, &result)

	if result.Account.Balance <= 0 {
		t.Fatalf("expected balance > 0, got %d", result.Account.Balance)
	}
	if result.Account.Balance >= 500000 {
		t.Fatalf("expected balance to be deducted below initial topup, got %d", result.Account.Balance)
	}
	t.Logf("current balance=%d", result.Account.Balance)
}

func stepVerifyLedger(t *testing.T, ctx context.Context, state *e2eState) {
	conn := grpcDial(t, billingGRPCEndpoint)
	defer conn.Close()
	client := billingv1.NewBillingServiceClient(conn)

	resp, err := client.ListLedger(ctx, &billingv1.ListLedgerRequest{
		UserId:   fmt.Sprintf("%d", state.userID),
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("list ledger failed: %v", err)
	}
	if len(resp.Entries) == 0 {
		t.Fatal("no ledger entries found after chat completion")
	}

	hasConsume := false
	for _, e := range resp.Entries {
		if e.Type == "consume" {
			hasConsume = true
			break
		}
	}
	if !hasConsume {
		t.Fatal("no consume entry in ledger")
	}
	t.Logf("ledger: %d entries, has consume entry", len(resp.Entries))
}

// ═══════════════════════════════════════════════════
// Phase 4: Admin - User Management
// ═══════════════════════════════════════════════════

func stepAdminAccess(t *testing.T) {
	body := httpGetWithAuth(t, adminHTTPBase+"/api/admin/access", adminToken)

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Admin bool `json:"admin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success || !result.Data.Admin {
		t.Fatalf("admin access denied: %s", string(body))
	}
}

func stepAdminListUsers(t *testing.T) {
	body := httpGetWithAuth(t, adminHTTPBase+"/v1/users?page=1&page_size=10", adminToken)

	var result struct {
		Users []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.Total == 0 {
		t.Fatal("expected at least one user")
	}
	t.Logf("listed %d users", result.Total)
}

func stepAdminGetUser(t *testing.T, state *e2eState) {
	url := fmt.Sprintf("%s/api/user/%d", adminHTTPBase, state.userID)
	body := httpGetWithAuth(t, url, adminToken)

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success {
		t.Fatalf("get user failed: %s", string(body))
	}
	if result.Data.Username != testUsername {
		t.Fatalf("username mismatch: expected %s, got %s", testUsername, result.Data.Username)
	}
	t.Logf("get user: id=%d, username=%s", result.Data.ID, result.Data.Username)
}

func stepAdminUpdateUser(t *testing.T, state *e2eState) {
	payload, _ := json.Marshal(map[string]interface{}{
		"user_id":      state.userID,
		"display_name": "E2E Test User Updated",
	})

	req, _ := http.NewRequest("PUT", adminHTTPBase+"/v1/users", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update user failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success {
		t.Fatalf("update user not successful: %s", body)
	}
	t.Logf("updated user %d display_name", state.userID)
}

// ═══════════════════════════════════════════════════
// Phase 5: Admin - Channel Management
// ═══════════════════════════════════════════════════

func stepAdminCreateProviderChannel(t *testing.T) {
	payload, _ := json.Marshal(map[string]interface{}{
		"name":     "e2e-real-provider",
		"type":     1, // OpenAI-compatible
		"base_url": providerBaseURL,
		"key":      providerAPIKey,
		"models":   providerModel,
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
	body := readBody(t, resp)

	var result struct {
		Success   bool  `json:"success"`
		ChannelID int64 `json:"channel_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success {
		t.Fatalf("create channel not successful: %s", body)
	}
	t.Logf("created provider channel: id=%d, base_url=%s, model=%s", result.ChannelID, providerBaseURL, providerModel)
}

func stepAdminListChannels(t *testing.T) {
	body := httpGetWithAuth(t, adminHTTPBase+"/v1/channels?page=1&page_size=10", adminToken)

	var result struct {
		Channels []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"channels"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	t.Logf("listed %d channels", result.Total)
}

// ═══════════════════════════════════════════════════
// Phase 6: Admin - Redeem Codes
// ═══════════════════════════════════════════════════

var testRedeemCode = "E2E-TEST-CODE-001"

func stepAdminCreateRedeemCode(t *testing.T) {
	payload, _ := json.Marshal(map[string]interface{}{
		"code":   testRedeemCode,
		"name":   "e2e-test-code",
		"amount": 100000,
		"count":  1,
	})

	req, _ := http.NewRequest("POST", adminHTTPBase+"/v1/redeem-codes", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create redeem code failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success {
		t.Fatalf("create redeem code not successful: %s", body)
	}
	t.Logf("created redeem code: %s", testRedeemCode)
}

func stepAdminListRedeemCodes(t *testing.T) {
	body := httpGetWithAuth(t, adminHTTPBase+"/v1/redeem-codes?page=1&page_size=10", adminToken)

	var result struct {
		Codes []struct {
			Code string `json:"code"`
		} `json:"codes"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.Total == 0 {
		t.Fatal("expected at least one redeem code")
	}
	t.Logf("listed %d redeem codes", result.Total)
}

func stepAdminGetRedeemCode(t *testing.T) {
	body := httpGetWithAuth(t, adminHTTPBase+"/v1/redeem-codes/"+testRedeemCode, adminToken)

	var result struct {
		RedeemCode struct {
			Code string `json:"code"`
		} `json:"redeem_code"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.RedeemCode.Code != testRedeemCode {
		t.Fatalf("code mismatch: expected %s, got %s", testRedeemCode, result.RedeemCode.Code)
	}
	t.Logf("get redeem code: %s", result.RedeemCode.Code)
}

func stepAdminDeleteRedeemCode(t *testing.T) {
	req, _ := http.NewRequest("DELETE", adminHTTPBase+"/v1/redeem-codes/"+testRedeemCode, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete redeem code failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success {
		t.Fatalf("delete redeem code not successful: %s", body)
	}
	t.Logf("deleted redeem code: %s", testRedeemCode)
}

// ═══════════════════════════════════════════════════
// Phase 7: Admin - System
// ═══════════════════════════════════════════════════

func stepAdminSystemOptions(t *testing.T) {
	body := httpGetWithAuth(t, adminHTTPBase+"/v1/system/options", adminToken)

	var result struct {
		Options struct {
			RegisterEnabled bool `json:"register_enabled"`
		} `json:"options"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		// Some implementations may return different structure, just log
		t.Logf("system options response: %s", truncate(string(body), 200))
		return
	}
	t.Logf("system options: register_enabled=%v", result.Options.RegisterEnabled)
}

func stepAdminLogs(t *testing.T, state *e2eState) {
	url := fmt.Sprintf("%s/v1/logs?user_id=%d&page_size=10", adminHTTPBase, state.userID)
	body := httpGetWithAuth(t, url, adminToken)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	t.Logf("admin logs response: keys=%d", len(result))
}

// ═══════════════════════════════════════════════════
// Phase 8: Admin - TopUp
// ═══════════════════════════════════════════════════

func stepAdminTopUp(t *testing.T, state *e2eState) {
	payload, _ := json.Marshal(map[string]interface{}{
		"user_id": fmt.Sprintf("%d", state.userID),
		"amount":  500000,
		"remark":  "e2e-test-topup",
	})

	req, _ := http.NewRequest("POST", adminHTTPBase+"/v1/topup", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("topup failed: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	var result struct {
		Success    bool  `json:"success"`
		NewBalance int64 `json:"new_balance"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Success {
		t.Fatalf("topup not successful: %s", body)
	}
	t.Logf("topup: new_balance=%d", result.NewBalance)
}

// ═══════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════

func grpcDial(t *testing.T, endpoint string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("gRPC dial %s: %v", endpoint, err)
	}
	return conn
}

func httpGetWithAuth(t *testing.T, url, token string) []byte {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	return readBody(t, resp)
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return body
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
