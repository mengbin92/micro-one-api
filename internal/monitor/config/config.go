package config

import appregistry "micro-one-api/internal/pkg/registry"

// Config holds the monitor-worker configuration.
type Config struct {
	Server   ServerConfig       `json:"server"`
	Data     DataConfig         `json:"data"`
	Monitor  MonitorSVCConfig   `json:"monitor_svc"`
	Clients  ClientsConfig      `json:"clients"`
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

type ClientsConfig struct {
	Channel ChannelClientConfig `json:"channel"`
}

type ChannelClientConfig struct {
	Endpoint string `json:"endpoint"`
}

// MonitorSVCConfig holds monitor-worker-specific settings.
type MonitorSVCConfig struct {
	// CollectIntervalSec is the metrics collection interval in seconds.
	CollectIntervalSec int `json:"collect_interval_sec"`
	// AlertRetentionDays is how many days to keep alert history.
	AlertRetentionDays int `json:"alert_retention_days"`
	// NotifyEmail is the recipient for alert notifications.
	NotifyEmail string `json:"notify_email"`
	// ChannelHealthCheckEnabled enables periodic upstream channel probes.
	ChannelHealthCheckEnabled bool `json:"channel_health_check_enabled"`
	// ChannelHealthCheckInterval is the probe interval (for example "5m").
	ChannelHealthCheckInterval string `json:"channel_health_check_interval"`
	// ChannelHealthCheckTimeout is the per-channel probe timeout.
	ChannelHealthCheckTimeout string `json:"channel_health_check_timeout"`
}
