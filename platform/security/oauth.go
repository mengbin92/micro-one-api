package oauth

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

// UserInfo holds the OAuth user profile returned by providers.
type UserInfo struct {
	Provider    string
	ProviderID  string
	Username    string
	Email       string
	DisplayName string
	AvatarURL   string
}

// Provider is the interface for OAuth2 identity providers.
type Provider interface {
	// Name returns the provider name (e.g. "github", "google").
	Name() string
	// AuthURL returns the URL to redirect the user for authorization.
	AuthURL(state string) string
	// Exchange exchanges an authorization code for user info.
	Exchange(ctx context.Context, code string) (*UserInfo, error)
}

// Config holds OAuth provider configuration.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type OIDCConfig struct {
	Config
	AuthorizeURL string
	TokenURL     string
	UserInfoURL  string
	Scopes       []string
}

type EndpointConfig struct {
	Config
	AuthorizeURL string
	TokenURL     string
	UserInfoURL  string
}

// ProviderRegistry holds registered OAuth providers.
type ProviderRegistry struct {
	providers map[string]Provider
}

// NewProviderRegistry creates a new registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]Provider)}
}

// Register adds a provider to the registry.
func (r *ProviderRegistry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// Get returns a provider by name.
func (r *ProviderRegistry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Names returns all registered provider names.
func (r *ProviderRegistry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// --- GitHub Provider ---

type githubProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client
}

// NewGitHubProvider creates a GitHub OAuth2 provider.
func NewGitHubProvider(cfg Config) Provider {
	return &githubProvider{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURL:  cfg.RedirectURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *githubProvider) Name() string { return "github" }

func (p *githubProvider) AuthURL(state string) string {
	params := url.Values{
		"client_id":    {p.clientID},
		"redirect_uri": {p.redirectURL},
		"scope":        {"read:user user:email"},
		"state":        {state},
	}
	return "https://github.com/login/oauth/authorize?" + params.Encode()
}

func (p *githubProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	// Exchange code for access token. Secrets go in the form body, NOT the URL
	// query: query strings are logged by proxies, WAFs, and access logs
	// (platform-L2).
	form := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"code":          {code},
	}
	tokenReq, err := http.NewRequestWithContext(ctx, "POST",
		"https://github.com/login/oauth/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("github token request: %w", err)
	}
	tokenReq.Header.Set("Accept", "application/json")
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(tokenReq)
	if err != nil {
		return nil, fmt.Errorf("github token exchange: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("github token decode: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("github oauth error: %s", tokenResp.Error)
	}

	// Fetch user profile
	userReq, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userReq.Header.Set("Accept", "application/json")

	userResp, err := p.httpClient.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("github user fetch: %w", err)
	}
	defer userResp.Body.Close()

	var ghUser struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&ghUser); err != nil {
		return nil, fmt.Errorf("github user decode: %w", err)
	}

	// If email is empty, fetch from emails endpoint
	if ghUser.Email == "" {
		ghUser.Email = p.fetchGitHubEmail(ctx, tokenResp.AccessToken)
	}

	displayName := ghUser.Name
	if displayName == "" {
		displayName = ghUser.Login
	}

	return &UserInfo{
		Provider:    "github",
		ProviderID:  fmt.Sprintf("%d", ghUser.ID),
		Username:    ghUser.Login,
		Email:       ghUser.Email,
		DisplayName: displayName,
		AvatarURL:   ghUser.AvatarURL,
	}, nil
}

func (p *githubProvider) fetchGitHubEmail(ctx context.Context, token string) string {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &emails); err != nil {
		return ""
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}
	return ""
}

// --- Google Provider ---

type googleProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client
}

// NewGoogleProvider creates a Google OAuth2 provider.
func NewGoogleProvider(cfg Config) Provider {
	return &googleProvider{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURL:  cfg.RedirectURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *googleProvider) Name() string { return "google" }

func (p *googleProvider) AuthURL(state string) string {
	params := url.Values{
		"client_id":     {p.clientID},
		"redirect_uri":  {p.redirectURL},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"access_type":   {"offline"},
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

func (p *googleProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	// Exchange code for access token
	data := url.Values{
		"code":          {code},
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"redirect_uri":  {p.redirectURL},
		"grant_type":    {"authorization_code"},
	}

	// Build the request with NewRequestWithContext so the caller's deadline/
	// cancellation propagates (PostForm does not honor ctx). Secrets are already
	// in the form body, not the URL query (platform-L2).
	tokenReq, err := http.NewRequestWithContext(ctx, "POST",
		"https://oauth2.googleapis.com/token",
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("google token request: %w", err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(tokenReq)
	if err != nil {
		return nil, fmt.Errorf("google token exchange: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("google token decode: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("google oauth error: %s", tokenResp.Error)
	}

	// Fetch user profile
	userReq, _ := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

	userResp, err := p.httpClient.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("google user fetch: %w", err)
	}
	defer userResp.Body.Close()

	var gUser struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&gUser); err != nil {
		return nil, fmt.Errorf("google user decode: %w", err)
	}

	username := gUser.Email
	if idx := strings.Index(username, "@"); idx > 0 {
		username = username[:idx]
	}

	return &UserInfo{
		Provider:    "google",
		ProviderID:  gUser.ID,
		Username:    username,
		Email:       gUser.Email,
		DisplayName: gUser.Name,
		AvatarURL:   gUser.Picture,
	}, nil
}

type oidcProvider struct {
	cfg        OIDCConfig
	httpClient *http.Client
}

func NewOIDCProvider(cfg OIDCConfig) Provider {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}
	return &oidcProvider{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type larkProvider struct {
	cfg        EndpointConfig
	httpClient *http.Client
}

func NewLarkProvider(cfg EndpointConfig) Provider {
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = "https://passport.feishu.cn/suite/passport/oauth/authorize"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	}
	if cfg.UserInfoURL == "" {
		cfg.UserInfoURL = "https://passport.feishu.cn/suite/passport/oauth/userinfo"
	}
	return &larkProvider{cfg: cfg, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (p *larkProvider) Name() string { return "lark" }

func (p *larkProvider) AuthURL(state string) string {
	params := url.Values{
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURL},
		"response_type": {"code"},
		"state":         {state},
	}
	return p.cfg.AuthorizeURL + "?" + params.Encode()
}

func (p *larkProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	body := map[string]string{
		"client_id":     p.cfg.ClientID,
		"client_secret": p.cfg.ClientSecret,
		"code":          code,
		"grant_type":    "authorization_code",
		"redirect_uri":  p.cfg.RedirectURL,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lark token exchange: %w", err)
	}
	defer resp.Body.Close()
	var tokenResp struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("lark token decode: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("lark oauth error: %s", tokenResp.ErrorDescription)
	}
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := p.httpClient.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("lark user fetch: %w", err)
	}
	defer userResp.Body.Close()
	var user struct {
		UnionID string `json:"union_id"`
		OpenID  string `json:"open_id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Avatar  string `json:"avatar_url"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("lark user decode: %w", err)
	}
	if user.Error != "" {
		return nil, fmt.Errorf("lark user error: %s", user.Error)
	}
	providerID := user.UnionID
	if providerID == "" {
		providerID = user.OpenID
	}
	username := usernameFromEmail(user.Email, "lark-"+providerID)
	displayName := user.Name
	if displayName == "" {
		displayName = username
	}
	return &UserInfo{Provider: "lark", ProviderID: providerID, Username: username, Email: user.Email, DisplayName: displayName, AvatarURL: user.Avatar}, nil
}

type wechatProvider struct {
	cfg        EndpointConfig
	httpClient *http.Client
}

func NewWeChatProvider(cfg EndpointConfig) Provider {
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = "https://open.weixin.qq.com/connect/qrconnect"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://api.weixin.qq.com/sns/oauth2/access_token"
	}
	if cfg.UserInfoURL == "" {
		cfg.UserInfoURL = "https://api.weixin.qq.com/sns/userinfo"
	}
	return &wechatProvider{cfg: cfg, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (p *wechatProvider) Name() string { return "wechat" }

func (p *wechatProvider) AuthURL(state string) string {
	params := url.Values{
		"appid":         {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURL},
		"response_type": {"code"},
		"scope":         {"snsapi_login"},
		"state":         {state},
	}
	return p.cfg.AuthorizeURL + "?" + params.Encode()
}

func (p *wechatProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	tokenURL, err := url.Parse(p.cfg.TokenURL)
	if err != nil {
		return nil, err
	}
	q := tokenURL.Query()
	q.Set("appid", p.cfg.ClientID)
	q.Set("secret", p.cfg.ClientSecret)
	q.Set("code", code)
	q.Set("grant_type", "authorization_code")
	tokenURL.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wechat token exchange: %w", err)
	}
	defer resp.Body.Close()
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		OpenID      string `json:"openid"`
		UnionID     string `json:"unionid"`
		ErrorCode   int    `json:"errcode"`
		ErrorMsg    string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("wechat token decode: %w", err)
	}
	if tokenResp.ErrorCode != 0 {
		return nil, fmt.Errorf("wechat oauth error: %s", tokenResp.ErrorMsg)
	}
	userURL, err := url.Parse(p.cfg.UserInfoURL)
	if err != nil {
		return nil, err
	}
	uq := userURL.Query()
	uq.Set("access_token", tokenResp.AccessToken)
	uq.Set("openid", tokenResp.OpenID)
	userURL.RawQuery = uq.Encode()
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, userURL.String(), nil)
	if err != nil {
		return nil, err
	}
	userResp, err := p.httpClient.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("wechat user fetch: %w", err)
	}
	defer userResp.Body.Close()
	var user struct {
		OpenID     string `json:"openid"`
		UnionID    string `json:"unionid"`
		Nickname   string `json:"nickname"`
		HeadImgURL string `json:"headimgurl"`
		ErrorCode  int    `json:"errcode"`
		ErrorMsg   string `json:"errmsg"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("wechat user decode: %w", err)
	}
	if user.ErrorCode != 0 {
		return nil, fmt.Errorf("wechat user error: %s", user.ErrorMsg)
	}
	providerID := user.UnionID
	if providerID == "" {
		providerID = tokenResp.UnionID
	}
	if providerID == "" {
		providerID = user.OpenID
	}
	if providerID == "" {
		providerID = tokenResp.OpenID
	}
	username := "wechat-" + providerID
	displayName := user.Nickname
	if displayName == "" {
		displayName = username
	}
	return &UserInfo{Provider: "wechat", ProviderID: providerID, Username: username, DisplayName: displayName, AvatarURL: user.HeadImgURL}, nil
}

func usernameFromEmail(email, fallback string) string {
	username := email
	if idx := strings.Index(username, "@"); idx > 0 {
		username = username[:idx]
	}
	if username == "" {
		username = fallback
	}
	return username
}

func (p *oidcProvider) Name() string { return "oidc" }

func (p *oidcProvider) AuthURL(state string) string {
	params := url.Values{
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURL},
		"response_type": {"code"},
		"scope":         {strings.Join(p.cfg.Scopes, " ")},
		"state":         {state},
	}
	return p.cfg.AuthorizeURL + "?" + params.Encode()
}

func (p *oidcProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	data := url.Values{
		"code":          {code},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
		"redirect_uri":  {p.cfg.RedirectURL},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc token exchange: %w", err)
	}
	defer resp.Body.Close()
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("oidc token decode: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("oidc oauth error: %s", tokenResp.Error)
	}
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := p.httpClient.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("oidc user fetch: %w", err)
	}
	defer userResp.Body.Close()
	var user struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("oidc user decode: %w", err)
	}
	username := user.Email
	if idx := strings.Index(username, "@"); idx > 0 {
		username = username[:idx]
	}
	if username == "" {
		username = "oidc-" + user.Sub
	}
	displayName := user.Name
	if displayName == "" {
		displayName = username
	}
	return &UserInfo{
		Provider:    "oidc",
		ProviderID:  user.Sub,
		Username:    username,
		Email:       user.Email,
		DisplayName: displayName,
		AvatarURL:   user.Picture,
	}, nil
}
