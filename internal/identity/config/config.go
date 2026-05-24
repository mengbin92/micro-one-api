package config

import appregistry "micro-one-api/internal/pkg/registry"

// Config holds the identity-service configuration.
type Config struct {
	Server       ServerConfig       `json:"server"`
	Data         DataConfig         `json:"data"`
	Clients      ClientsConfig      `json:"clients"`
	OAuth        OAuthConfig        `json:"oauth"`
	Registration RegistrationConfig `json:"registration"`
	Registry     appregistry.Config `json:"registry"`
}

type ServerConfig struct {
	HTTP HTTPConfig `json:"http"`
	GRPC GRPCConfig `json:"grpc"`
}

type HTTPConfig struct {
	Addr string `json:"addr"`
}

type GRPCConfig struct {
	Addr string `json:"addr"`
}

type DataConfig struct {
	Database DatabaseConfig `json:"database"`
	Redis    RedisConfig    `json:"redis"`
}

type DatabaseConfig struct {
	Driver string `json:"driver"`
	Source string `json:"source"`
}

type RedisConfig struct {
	Addr string `json:"addr"`
}

type ClientsConfig struct {
	Billing BillingClientConfig `json:"billing"`
}

type BillingClientConfig struct {
	Endpoint string `json:"endpoint"`
}

type OAuthConfig struct {
	GitHub  OAuthProviderConfig `json:"github"`
	Google  OAuthProviderConfig `json:"google"`
	OIDC    OIDCProviderConfig  `json:"oidc"`
	Lark    OAuthProviderConfig `json:"lark"`
	WeChat  OAuthProviderConfig `json:"wechat"`
	BaseURL string              `json:"base_url"`
}

type OAuthProviderConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Enabled      bool   `json:"enabled"`
}

type OIDCProviderConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Enabled      bool     `json:"enabled"`
	AuthorizeURL string   `json:"authorize_url"`
	TokenURL     string   `json:"token_url"`
	UserInfoURL  string   `json:"user_info_url"`
	Scopes       []string `json:"scopes"`
}

type RegistrationConfig struct {
	Enabled                       bool     `json:"enabled"`
	Disabled                      bool     `json:"disabled"`
	EmailDomainRestrictionEnabled bool     `json:"email_domain_restriction_enabled"`
	EmailDomainWhitelist          []string `json:"email_domain_whitelist"`
	TurnstileCheckEnabled         bool     `json:"turnstile_check_enabled"`
	TurnstileSecret               string   `json:"turnstile_secret"`
}
