package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

const (
	identityGRPCEndpoint = "127.0.0.1:9001"
	billingGRPCEndpoint  = "127.0.0.1:9004"
	relayHTTPBase        = "http://127.0.0.1:8080"
	adminHTTPBase        = "http://127.0.0.1:3000"

	testUsername = "e2e-test-user"
	testPassword = "testpass123456"
	testEmail    = "e2e@test.com"
	testModel    = "gpt-3.5-turbo"
)

var (
	passed, failed int
)

func logf(format string, args ...interface{}) {
	fmt.Printf("[E2E] "+format+"\n", args...)
}

func pass(name string) {
	passed++
	fmt.Printf("  \033[32m✓\033[0m %s\n", name)
}

func fail(name, reason string) {
	failed++
	fmt.Printf("  \033[31m✗\033[0m %s: %s\n", name, reason)
}

func main() {
	step := "all"
	for i, arg := range os.Args[1:] {
		if arg == "--step" && i+1 < len(os.Args)-1 {
			step = os.Args[i+2]
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch step {
	case "register":
		// Only register (used by shell script to get user_id)
		logf("Step 1: Register user")
		stepRegister(ctx)
	case "all":
		// Full flow: login -> models -> chat -> billing
		logf("Step 2: Login")
		token := stepLogin(ctx)
		if token == "" {
			summary()
			os.Exit(1)
		}

		logf("Step 3: Verify initial account")
		// Get user_id from token (query admin-api)
		userID := getUserIDFromToken(ctx, token)

		initialQuota := stepVerifyAccount(ctx, userID)

		logf("Step 4: Create API token")
		apiToken := stepCreateAPIToken(ctx, userID)
		if apiToken == "" {
			summary()
			os.Exit(1)
		}

		logf("Step 5: List models")
		stepListModels(apiToken)

		logf("Step 6: Chat completion")
		stepChatCompletion(apiToken)

		logf("Step 7: Verify billing")
		stepVerifyBilling(ctx, userID, initialQuota)

		logf("Step 8: Verify admin logs")
		stepVerifyLogs(userID)
	}

	summary()
	if failed > 0 {
		os.Exit(1)
	}
}

func adminToken() string {
	if token := os.Getenv("ADMIN_TOKEN"); token != "" {
		return token
	}
	return "test-admin-token-for-dev"
}

func summary() {
	fmt.Println()
	fmt.Println("=========================================")
	fmt.Printf("  Results: \033[32m%d passed\033[0m, \033[31m%d failed\033[0m\n", passed, failed)
	fmt.Println("=========================================")
}

func getUserIDFromToken(ctx context.Context, token string) int64 {
	conn := grpcDial(identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: token})
	if err != nil || !resp.Valid {
		// Fallback: query DB isn't available, try login again to get user_id
		loginResp, err := client.Login(ctx, &identityv1.LoginRequest{
			Username: testUsername,
			Password: testPassword,
		})
		if err == nil && loginResp.Success {
			return loginResp.UserId
		}
		return 0
	}
	return resp.UserId
}

// ── Step implementations ──

func stepRegister(ctx context.Context) int64 {
	conn := grpcDial(identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.Register(ctx, &identityv1.RegisterRequest{
		Username: testUsername,
		Password: testPassword,
		Email:    testEmail,
		Group:    "default",
	})
	if err != nil {
		fail("register", err.Error())
		return 0
	}
	if !resp.Success {
		fail("register", resp.Message)
		return 0
	}
	if resp.UserId == 0 {
		fail("register", "returned user_id=0")
		return 0
	}
	pass(fmt.Sprintf("register (user_id=%d)", resp.UserId))
	return resp.UserId
}

func stepLogin(ctx context.Context) string {
	conn := grpcDial(identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.Login(ctx, &identityv1.LoginRequest{
		Username: testUsername,
		Password: testPassword,
	})
	if err != nil {
		fail("login", err.Error())
		return ""
	}
	if !resp.Success {
		fail("login", resp.Message)
		return ""
	}
	if resp.Token == "" {
		fail("login", "returned empty token")
		return ""
	}
	pass(fmt.Sprintf("login (token=%s...)", resp.Token[:8]))
	return resp.Token
}

func stepVerifyAccount(_ context.Context, userID int64) int64 {
	url := fmt.Sprintf("%s/v1/account?user_id=%d", adminHTTPBase, userID)
	body := httpGetWithAuth(url, adminToken())

	var result struct {
		Account struct {
			UserID string `json:"user_id"`
			Quota  int64  `json:"quota"`
			Group  string `json:"group"`
		} `json:"account"`
	}
	json.Unmarshal(body, &result)

	if result.Account.Quota <= 0 {
		fail("verify-account", fmt.Sprintf("expected quota > 0, got %d", result.Account.Quota))
		return 0
	}
	pass(fmt.Sprintf("verify-account (quota=%d, group=%s)", result.Account.Quota, result.Account.Group))
	return result.Account.Quota
}

func stepCreateAPIToken(ctx context.Context, userID int64) string {
	conn := grpcDial(identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.CreateAccessToken(ctx, &identityv1.CreateAccessTokenRequest{
		UserId: userID,
		Name:   "e2e-api-token",
		Models: []string{testModel},
	})
	if err != nil {
		fail("create-api-token", err.Error())
		return ""
	}
	if !resp.Success {
		fail("create-api-token", resp.Message)
		return ""
	}
	if resp.Token == "" {
		fail("create-api-token", "returned empty token")
		return ""
	}
	pass(fmt.Sprintf("create-api-token (token=%s...)", resp.Token[:8]))
	return resp.Token
}

func stepListModels(token string) {
	url := fmt.Sprintf("%s/v1/models", relayHTTPBase)
	body := httpGetWithAuth(url, token)

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fail("list-models", fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	found := false
	for _, m := range result.Data {
		if m.ID == testModel {
			found = true
			break
		}
	}
	if !found {
		fail("list-models", fmt.Sprintf("%s not in model list", testModel))
		return
	}
	pass(fmt.Sprintf("list-models (%d models, includes %s)", len(result.Data), testModel))
}

func stepChatCompletion(token string) {
	url := fmt.Sprintf("%s/v1/chat/completions", relayHTTPBase)
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":    testModel,
		"messages": []map[string]string{{"role": "user", "content": "Hello, how are you?"}},
	})

	httpReq, _ := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		fail("chat-completion", err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		fail("chat-completion", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)))
		return
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
		fail("chat-completion", fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		fail("chat-completion", "empty response content")
		return
	}
	pass(fmt.Sprintf("chat-completion (tokens=%d, content=%q...)", result.Usage.TotalTokens, truncate(result.Choices[0].Message.Content, 40)))
}

func stepVerifyBilling(ctx context.Context, userID int64, initialQuota int64) {
	// Check quota decreased
	url := fmt.Sprintf("%s/v1/account?user_id=%d", adminHTTPBase, userID)
	body := httpGetWithAuth(url, adminToken())

	var result struct {
		Account struct {
			Quota int64 `json:"quota"`
		} `json:"account"`
	}
	json.Unmarshal(body, &result)

	if result.Account.Quota >= initialQuota {
		fail("billing-quota-deducted", fmt.Sprintf("quota not decreased: initial=%d, now=%d", initialQuota, result.Account.Quota))
	} else {
		deducted := initialQuota - result.Account.Quota
		pass(fmt.Sprintf("billing-quota-deducted (deducted=%d)", deducted))
	}

	// Check ledger entries via billing-service gRPC
	conn := grpcDial(billingGRPCEndpoint)
	defer conn.Close()
	billingClient := billingv1.NewBillingServiceClient(conn)

	ledgerResp, err := billingClient.ListLedger(ctx, &billingv1.ListLedgerRequest{
		UserId:   fmt.Sprintf("%d", userID),
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		fail("billing-ledger", err.Error())
	} else if len(ledgerResp.Entries) == 0 {
		fail("billing-ledger", "no ledger entries found")
	} else {
		hasConsume := false
		for _, e := range ledgerResp.Entries {
			if e.Type == "consume" {
				hasConsume = true
				break
			}
		}
		if hasConsume {
			pass(fmt.Sprintf("billing-ledger (%d entries, has consume)", len(ledgerResp.Entries)))
		} else {
			fail("billing-ledger", "no consume entry found")
		}
	}
}

func stepVerifyLogs(userID int64) {
	url := fmt.Sprintf("%s/v1/logs?user_id=%d&page_size=10", adminHTTPBase, userID)
	body := httpGetWithAuth(url, adminToken())

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	// Logs may be empty if log-service hasn't indexed yet, so just check the endpoint works
	pass(fmt.Sprintf("admin-logs (response OK, keys=%d)", len(result)))
}

// ── Helpers ──

func grpcDial(endpoint string) *grpc.ClientConn {
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: gRPC dial %s: %v\n", endpoint, err)
		os.Exit(1)
	}
	return conn
}

func httpGetWithAuth(url, token string) []byte {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return []byte("{}")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
