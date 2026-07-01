package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	"micro-one-api/internal/channel/biz"
	relaycredential "micro-one-api/internal/relay/credential"
)

const (
	PlatformClaude = "claude"
	PlatformCodex  = "codex"

	claudeAuthorizeURL = "https://claude.ai/oauth/authorize"
	claudeRedirectURI  = "https://platform.claude.com/oauth/code/callback"
	claudeScope        = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"

	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexRedirectURI  = "http://localhost:1455/auth/callback"
	codexScope        = "openid profile email offline_access"
)

var (
	chatGPTAccountsCheckURL = "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"
	chatGPTPrivacyURL       = "https://chatgpt.com/backend-api/settings/user"
)

var ErrInvalidSession = errors.New("invalid oauth session")

type ChannelUsecase interface {
	CreateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error
}

type Service struct {
	uc     ChannelUsecase
	store  *SessionStore
	client *http.Client
	now    func() time.Time

	tokenURLs map[string]string
}

type AuthURLRequest struct {
	RedirectURI string
}

type AuthURLResult struct {
	AuthURL   string
	SessionID string
	State     string
	ExpiresAt int64
}

type ExchangeRequest struct {
	SessionID string
	State     string
	Code      string

	Name     string
	Group    string
	Models   string
	Priority int64
	BaseURL  string
	Metadata string
}

type ExchangeResult struct {
	AccountID int64
	Platform  string
	Metadata  string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	Organization *struct {
		UUID string `json:"uuid"`
	} `json:"organization"`
	Account *struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

func NewService(uc ChannelUsecase, opts ...Option) *Service {
	s := &Service{
		uc:     uc,
		store:  NewSessionStore(defaultSessionTTL),
		client: &http.Client{Timeout: 30 * time.Second},
		now:    time.Now,
		tokenURLs: map[string]string{
			PlatformClaude: relaycredential.ClaudeTokenRefreshURL,
			PlatformCodex:  relaycredential.CodexTokenRefreshURL,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type Option func(*Service)

func WithHTTPClient(client *http.Client) Option {
	return func(s *Service) {
		if client != nil {
			s.client = client
		}
	}
}

func WithSessionStore(store *SessionStore) Option {
	return func(s *Service) {
		if store != nil {
			s.store = store
		}
	}
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func WithTokenURL(platform, tokenURL string) Option {
	return func(s *Service) {
		platform = normalizePlatform(platform)
		if platform != "" && strings.TrimSpace(tokenURL) != "" {
			s.tokenURLs[platform] = strings.TrimSpace(tokenURL)
		}
	}
}

func (s *Service) AuthURL(ctx context.Context, platform string, req AuthURLRequest) (*AuthURLResult, error) {
	platform = normalizePlatform(platform)
	if platform == "" {
		return nil, fmt.Errorf("unsupported oauth platform")
	}
	state, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	sessionID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	verifier, err := codeVerifier(platform)
	if err != nil {
		return nil, err
	}
	redirectURI := strings.TrimSpace(req.RedirectURI)
	if redirectURI == "" {
		redirectURI = defaultRedirectURI(platform)
	}
	session := &Session{
		ID:           sessionID,
		Platform:     platform,
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		CreatedAt:    s.now(),
	}
	s.store.Set(session)
	authURL := buildAuthURL(platform, state, codeChallenge(verifier), redirectURI)
	return &AuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
		State:     state,
		ExpiresAt: session.CreatedAt.Add(s.store.ttl).Unix(),
	}, nil
}

func (s *Service) Exchange(ctx context.Context, platform string, req ExchangeRequest) (*ExchangeResult, error) {
	if s == nil || s.uc == nil {
		return nil, fmt.Errorf("oauth service not configured")
	}
	platform = normalizePlatform(platform)
	session, ok := s.store.Pop(req.SessionID, s.now())
	if !ok || session.Platform != platform || session.State != req.State {
		return nil, ErrInvalidSession
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, fmt.Errorf("oauth code is required")
	}

	token, err := s.exchangeCode(ctx, session, code)
	if err != nil {
		return nil, err
	}
	account := s.subscriptionAccountFromToken(ctx, platform, token, req)
	if err := s.uc.CreateSubscriptionAccount(ctx, account); err != nil {
		return nil, err
	}
	return &ExchangeResult{
		AccountID: account.ID,
		Platform:  platform,
		Metadata:  account.Metadata,
	}, nil
}

func (s *Service) exchangeCode(ctx context.Context, session *Session, code string) (*tokenResponse, error) {
	tokenURL := s.tokenURLs[session.Platform]
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID(session.Platform))
	form.Set("code", code)
	form.Set("redirect_uri", session.RedirectURI)
	form.Set("code_verifier", session.CodeVerifier)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")
	if session.Platform == PlatformCodex {
		httpReq.Header.Set("User-Agent", "codex-cli/0.91.0")
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("oauth token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth token exchange failed: status=%d body=%s", resp.StatusCode, truncate(body))
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("decode oauth token response: %w", err)
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("oauth token exchange returned empty access_token")
	}
	return &token, nil
}

func (s *Service) subscriptionAccountFromToken(ctx context.Context, platform string, token *tokenResponse, req ExchangeRequest) *biz.SubscriptionAccount {
	metadata := metadataMap(req.Metadata)
	accountID := ""
	if platform == PlatformClaude {
		accountID = claudeAccountID(token)
	} else {
		accountID, metadata = codexAccountInfo(ctx, s.client, token.AccessToken, token.IDToken, metadata)
		disableOpenAITraining(ctx, s.client, token.AccessToken)
	}

	now := s.now()
	expiresAt := int64(0)
	if token.ExpiresIn > 0 {
		expiresAt = now.Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = platform + "-oauth-" + now.Format("20060102150405")
	}
	group := strings.TrimSpace(req.Group)
	if group == "" {
		group = "default"
	}
	models := biz.SplitCSV(req.Models)
	if len(models) == 0 {
		models = defaultModels(platform)
	}
	metadataJSON, _ := sonic.Marshal(metadata)
	return &biz.SubscriptionAccount{
		Name:         name,
		Platform:     platform,
		AccountType:  "oauth",
		Status:       biz.ChannelStatusEnabled,
		Group:        group,
		Models:       models,
		Priority:     req.Priority,
		BaseURL:      strings.TrimSpace(req.BaseURL),
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    expiresAt,
		AccountID:    accountID,
		Metadata:     string(metadataJSON),
		CreatedAt:    now.Unix(),
		UpdatedAt:    now.Unix(),
	}
}

func normalizePlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformClaude:
		return PlatformClaude
	case PlatformCodex, "openai":
		return PlatformCodex
	default:
		return ""
	}
}

func codeVerifier(platform string) (string, error) {
	if platform == PlatformCodex {
		return randomHex(64)
	}
	b, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func buildAuthURL(platform, state, challenge, redirectURI string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID(platform))
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", authScope(platform))
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	if platform == PlatformClaude {
		params.Set("code", "true")
		return claudeAuthorizeURL + "?" + params.Encode()
	}
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	return codexAuthorizeURL + "?" + params.Encode()
}

func clientID(platform string) string {
	if platform == PlatformClaude {
		return relaycredential.ClaudeOAuthClientID
	}
	return relaycredential.CodexOAuthClientID
}

func authScope(platform string) string {
	if platform == PlatformClaude {
		return claudeScope
	}
	return codexScope
}

func defaultRedirectURI(platform string) string {
	if platform == PlatformClaude {
		return claudeRedirectURI
	}
	return codexRedirectURI
}

func defaultModels(platform string) []string {
	if platform == PlatformClaude {
		return []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514", "claude-3-5-sonnet-20241022"}
	}
	return []string{"gpt-5", "gpt-5-codex", "codex-mini-latest", "o4-mini"}
}

func claudeAccountID(token *tokenResponse) string {
	if token == nil {
		return ""
	}
	if token.Organization != nil && token.Organization.UUID != "" {
		return token.Organization.UUID
	}
	if token.Account != nil && token.Account.UUID != "" {
		return token.Account.UUID
	}
	return ""
}

func metadataMap(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := sonic.UnmarshalString(raw, &m); err != nil {
		return map[string]any{"raw_metadata": raw}
	}
	return m
}

func truncate(body []byte) string {
	s := string(body)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
