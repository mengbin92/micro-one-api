package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"micro-one-api/pkg/jsonx"
	grpcauth "micro-one-api/platform/grpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1 "micro-one-api/api/admin/v1"
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

func logf(format string, args ...any) {
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
	step := flag.String("step", "all", "register a fixture user or run all authenticated checks")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch *step {
	case "register":
		logf("Step 1: Register user")
		stepRegister(ctx)
	case "all":
		// Full flow: login -> models -> chat -> billing
		logf("Step 2: Login")
		session, userID := stepLogin(ctx)
		if session == "" {
			break
		}
		logf("Step 3: Fund test account")
		initialBalance := stepTopUp(ctx, userID)
		if initialBalance <= 0 {
			break
		}

		logf("Step 4: Create API token")
		apiToken := stepCreateAPIToken(ctx, session)
		if apiToken == "" {
			break
		}
		logf("Step 5: List models")
		stepListModels(ctx, apiToken)

		logf("Step 6: Chat completion")
		stepChatCompletion(ctx, apiToken)

		logf("Step 7: Verify billing")
		stepVerifyBilling(ctx, userID, initialBalance)

		logf("Step 8: Verify admin logs")
		stepVerifyLogs(ctx, userID)
	default:
		fail("step", "expected register or all")
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

// ── Step implementations ──

func stepRegister(ctx context.Context) {
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
		return
	}
	if !resp.Success {
		fail("register", resp.Message)
		return
	}
	if resp.UserId == 0 {
		fail("register", "returned user_id=0")
		return
	}
	pass(fmt.Sprintf("register (user_id=%d)", resp.UserId))
}

func stepLogin(ctx context.Context) (string, int64) {
	conn := grpcDial(identityGRPCEndpoint)
	defer conn.Close()
	client := identityv1.NewIdentityServiceClient(conn)

	resp, err := client.Login(ctx, &identityv1.LoginRequest{
		Username: testUsername,
		Password: testPassword,
	})
	if err != nil {
		fail("login", err.Error())
		return "", 0
	}
	if !resp.Success {
		fail("login", resp.Message)
		return "", 0
	}
	if resp.Token == "" || resp.UserId <= 0 {
		fail("login", "missing session or user ID")
		return "", 0
	}
	pass("login")
	return resp.Token, resp.UserId
}

func stepTopUp(ctx context.Context, userID int64) int64 {
	payload := &adminv1.TopUpQuotaRequest{UserId: fmt.Sprint(userID), Amount: 1000000, Remark: "standalone E2E funding"}
	var out adminv1.TopUpQuotaResponse
	if err := requestJSON(ctx, adminHTTPBase+"/v1/topup", adminToken(), payload, &out); err != nil {
		fail("topup", err.Error())
		return 0
	}
	if !out.Success || out.NewBalance < payload.Amount {
		fail("topup", "funding was not confirmed")
		return 0
	}
	pass(fmt.Sprintf("topup (balance=%d)", out.NewBalance))
	return out.NewBalance
}

func stepCreateAPIToken(ctx context.Context, session string) string {
	payload := map[string]any{"name": "e2e-api-token", "models": []string{testModel}, "unlimited_quota": true}
	var out struct {
		Success bool
		Data    struct{ Key string }
	}
	if err := requestJSON(ctx, adminHTTPBase+"/api/token", session, payload, &out); err != nil {
		fail("create-api-token", err.Error())
		return ""
	}
	if !out.Success || out.Data.Key == "" {
		fail("create-api-token", "credential was not issued")
		return ""
	}
	pass("create-api-token")
	return out.Data.Key
}

func stepListModels(ctx context.Context, token string) {
	url := fmt.Sprintf("%s/v1/models", relayHTTPBase)

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := requestJSON(ctx, url, token, nil, &result); err != nil {
		fail("list-models", err.Error())
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

func stepChatCompletion(ctx context.Context, token string) {
	url := relayHTTPBase + "/v1/chat/completions"
	payload := map[string]any{"model": testModel, "messages": []map[string]string{{"role": "user", "content": "Hello, how are you?"}}}

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
	if err := requestJSON(ctx, url, token, payload, &result); err != nil {
		fail("chat-completion", err.Error())
		return
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		fail("chat-completion", "empty response content")
		return
	}
	pass(fmt.Sprintf("chat-completion (tokens=%d, content=%q...)", result.Usage.TotalTokens, truncate(result.Choices[0].Message.Content, 40)))
}

func stepVerifyBilling(ctx context.Context, userID int64, initialBalance int64) {
	url := fmt.Sprintf("%s/v1/account?user_id=%d", adminHTTPBase, userID)
	var result adminv1.AdminAccountSnapshotResponse
	if err := requestJSON(ctx, url, adminToken(), nil, &result); err != nil {
		fail("billing-balance", err.Error())
	} else if result.Account == nil || result.Account.UserId != fmt.Sprint(userID) || result.Account.Balance >= initialBalance {
		fail("billing-balance", "account does not show the expected debit")
	} else {
		pass(fmt.Sprintf("billing-balance (deducted=%d)", initialBalance-result.Account.Balance))
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

func stepVerifyLogs(ctx context.Context, userID int64) {
	url := fmt.Sprintf("%s/v1/logs?user_id=%d&page_size=10", adminHTTPBase, userID)
	var result struct{ Success bool }
	if err := requestJSON(ctx, url, adminToken(), nil, &result); err != nil {
		fail("admin-logs", err.Error())
	} else if !result.Success {
		fail("admin-logs", "log request was rejected")
	} else {
		pass("admin-logs")
	}
}

// ── Helpers ──

func grpcDial(endpoint string) *grpc.ClientConn {
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(os.Getenv("SERVICE_TOKEN"))),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: gRPC dial %s: %v\n", endpoint, err)
		os.Exit(1)
	}
	return conn
}

func requestJSON(ctx context.Context, url, token string, payload, out any) error {
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		raw, err := jsonx.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", req.URL.Path, resp.StatusCode)
	}
	return jsonx.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
