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
	// Exchange code for access token
	tokenReq, _ := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/oauth/access_token", nil)
	q := tokenReq.URL.Query()
	q.Set("client_id", p.clientID)
	q.Set("client_secret", p.clientSecret)
	q.Set("code", code)
	tokenReq.URL.RawQuery = q.Encode()
	tokenReq.Header.Set("Accept", "application/json")

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

	resp, err := p.httpClient.PostForm("https://oauth2.googleapis.com/token", data)
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
