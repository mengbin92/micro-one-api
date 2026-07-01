package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

func codexAccountInfo(ctx context.Context, client *http.Client, accessToken, idToken string, metadata map[string]any) (string, map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	accountID, planType := accountInfoFromIDToken(idToken)
	if accountID != "" {
		metadata["chatgpt_account_id"] = accountID
	}
	if planType != "" {
		metadata["plan_type"] = planType
	}
	if accountID == "" || planType == "" {
		if info := fetchChatGPTAccountInfo(ctx, client, accessToken, accountID); info != nil {
			if accountID == "" {
				accountID = info.accountID
			}
			if info.planType != "" {
				metadata["plan_type"] = info.planType
			}
			if info.email != "" {
				metadata["email"] = info.email
			}
			if info.expiresAt != "" {
				metadata["subscription_expires_at"] = info.expiresAt
			}
		}
	}
	return accountID, metadata
}

func accountInfoFromIDToken(idToken string) (string, string) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims map[string]any
	if err := sonic.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	accountID, _ := auth["chatgpt_account_id"].(string)
	planType, _ := auth["chatgpt_plan_type"].(string)
	return accountID, planType
}

type chatGPTAccountInfo struct {
	accountID string
	planType  string
	email     string
	expiresAt string
}

func fetchChatGPTAccountInfo(ctx context.Context, client *http.Client, accessToken, preferredAccountID string) *chatGPTAccountInfo {
	if client == nil || accessToken == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTAccountsCheckURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var result map[string]any
	if err := sonic.Unmarshal(body, &result); err != nil {
		return nil
	}
	accounts, _ := result["accounts"].(map[string]any)
	if len(accounts) == 0 {
		return nil
	}
	if preferredAccountID != "" {
		if acct, ok := accountMap(accounts[preferredAccountID]); ok {
			info := extractChatGPTAccountInfo(preferredAccountID, acct)
			if info.planType != "" {
				return info
			}
		}
	}
	var fallback *chatGPTAccountInfo
	for id, raw := range accounts {
		acct, ok := accountMap(raw)
		if !ok {
			continue
		}
		info := extractChatGPTAccountInfo(id, acct)
		if info.planType == "" {
			continue
		}
		if fallback == nil {
			fallback = info
		}
		if account, _ := acct["account"].(map[string]any); account != nil {
			if isDefault, _ := account["is_default"].(bool); isDefault {
				return info
			}
		}
		if !strings.EqualFold(info.planType, "free") {
			return info
		}
	}
	return fallback
}

func accountMap(raw any) (map[string]any, bool) {
	acct, ok := raw.(map[string]any)
	return acct, ok
}

func extractChatGPTAccountInfo(id string, acct map[string]any) *chatGPTAccountInfo {
	info := &chatGPTAccountInfo{accountID: id}
	info.planType = extractPlanType(acct)
	info.expiresAt = extractExpiresAt(acct)
	if account, _ := acct["account"].(map[string]any); account != nil {
		if email, _ := account["email"].(string); email != "" {
			info.email = email
		}
	}
	return info
}

func extractPlanType(acct map[string]any) string {
	for _, key := range []string{"plan_type", "planType"} {
		if value, _ := acct[key].(string); value != "" {
			return value
		}
	}
	if ent, _ := acct["entitlement"].(map[string]any); ent != nil {
		if value, _ := ent["subscription_plan"].(string); value != "" {
			return value
		}
		if value, _ := ent["plan_type"].(string); value != "" {
			return value
		}
	}
	if account, _ := acct["account"].(map[string]any); account != nil {
		if value, _ := account["plan_type"].(string); value != "" {
			return value
		}
	}
	return ""
}

func extractExpiresAt(acct map[string]any) string {
	if ent, _ := acct["entitlement"].(map[string]any); ent != nil {
		if value, _ := ent["expires_at"].(string); value != "" {
			return value
		}
	}
	return ""
}

func disableOpenAITraining(ctx context.Context, client *http.Client, accessToken string) {
	if client == nil || accessToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"training_allowed": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, chatGPTPrivacyURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
}
