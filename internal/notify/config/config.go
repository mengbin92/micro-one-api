package config

import appregistry "micro-one-api/internal/pkg/registry"

// Config holds the notify-worker configuration.
type Config struct {
	Server   ServerConfig       `json:"server"`
	Data     DataConfig         `json:"data"`
	Notify   NotifySVCConfig    `json:"notify_svc"`
	Registry appregistry.Config `json:"registry"`
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

// NotifySVCConfig holds notify-worker-specific settings.
type NotifySVCConfig struct {
	// WebhookURL is the default webhook endpoint for notifications.
	WebhookURL string `json:"webhook_url"`
	// SMTPHost is the SMTP server host for email notifications.
	SMTPHost string `json:"smtp_host"`
	// SMTPPort is the SMTP server port.
	SMTPPort int `json:"smtp_port"`
	// SMTPUser is the SMTP auth username.
	SMTPUser string `json:"smtp_user"`
	// SMTPPass is the SMTP auth password.
	SMTPPass string `json:"smtp_pass"`
	// SMTPFrom is the sender email address.
	SMTPFrom string `json:"smtp_from"`
	// DispatchInterval is the pending notification scan interval.
	DispatchInterval string `json:"dispatch_interval"`
	// DispatchBatch is the maximum pending notifications processed per scan.
	DispatchBatch int32 `json:"dispatch_batch"`
	// MaxRetry is the maximum send attempts before marking a notification failed.
	MaxRetry int `json:"max_retry"`
}
