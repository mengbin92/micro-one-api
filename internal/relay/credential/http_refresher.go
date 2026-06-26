package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tokenRefreshResponse is the JSON body returned by an OAuth token endpoint
// for a refresh_token grant.
type tokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// refresher performs the actual HTTP POST to an OAuth token endpoint to
// exchange a refresh_token for a new access_token. It is shared by the Claude
// and OpenAI token providers; the two differ only in the token endpoint URL,
// client_id and extra form parameters.
type refresher struct {
	httpClient *http.Client
	// clientID is the OAuth client_id. For Claude it is the published Claude
	// Code client_id; for Codex it is the ChatGPT client_id.
	clientID string
	// extraForm are appended to the standard grant_type=refresh_token body
	// (e.g. Claude's "client_secret" is empty for CC, Codex may add scope).
	extraForm url.Values
}

// refresh exchanges the given refresh token for a new access token. It returns
// the new credentials (new refresh token if the upstream rotated it) or an
// error wrapping ErrRefreshFailed.
func (r *refresher) refresh(ctx context.Context, refreshURL, refreshToken string) (*AccountCredentials, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", r.clientID)
	for k, vs := range r.extraForm {
		for _, v := range vs {
			form.Set(k, v)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrRefreshFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRefreshFailed, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MiB safety cap
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status=%d body=%s", ErrRefreshFailed, resp.StatusCode, string(body))
	}
	var tr tokenRefreshResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrRefreshFailed, err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty access_token", ErrRefreshFailed)
	}
	newRefresh := tr.RefreshToken
	if newRefresh == "" {
		newRefresh = refreshToken // upstream did not rotate the refresh token
	}
	expiresAt := time.Now()
	if tr.ExpiresIn > 0 {
		expiresAt = expiresAt.Add(time.Duration(tr.ExpiresIn) * time.Second)
	} else {
		expiresAt = expiresAt.Add(time.Hour) // conservative default
	}
	return &AccountCredentials{
		AccessToken:  tr.AccessToken,
		RefreshToken: newRefresh,
		ExpiresAt:    expiresAt,
	}, nil
}

// defaultRefreshHTTPClient builds an *http.Client with a sane timeout for token
// refresh calls. Token endpoints are fast and idempotent; a 30s cap protects
// against a stuck upstream.
func defaultRefreshHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
