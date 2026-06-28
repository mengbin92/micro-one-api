package config

import (
	"micro-one-api/internal/billing/biz"
	appregistry "micro-one-api/internal/pkg/registry"
)

// Config holds the billing-service configuration.
type Config struct {
	Server    ServerConfig       `json:"server"`
	Data      DataConfig         `json:"data"`
	Billing   BillingConfig      `json:"billing"`
	Payment   biz.PaymentConfig  `json:"payment"`
	Clients   ClientsConfig      `json:"clients"`
	Recon     ReconAlertConfig   `json:"recon"`
	Partition PartitionConfig    `json:"partition"`
	Registry  appregistry.Config `json:"registry"`
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

type BillingConfig struct {
	ReservationTimeout string             `json:"reservation_timeout"`
	GroupRatios        map[string]float64 `json:"group_ratios"`
	ModelRatios        map[string]float64 `json:"model_ratios"`
	CompletionRatios   map[string]float64 `json:"completion_ratios"`
	Async              AsyncBillingConfig `json:"async"`
}

type AsyncBillingConfig struct {
	Enabled       bool   `json:"enabled"`
	QueueSize     int    `json:"queue_size"`
	BatchSize     int    `json:"batch_size"`
	BatchInterval string `json:"batch_interval"`
}

// ClientsConfig holds gRPC endpoints for downstream services used by the
// billing service. An empty Endpoint disables the client (notify alerts will
// be silently dropped if notifier endpoint is empty).
type ClientsConfig struct {
	Notify NotifyClientConfig `json:"notify"`
}

type NotifyClientConfig struct {
	Endpoint   string `json:"endpoint"`
	NotifyType string `json:"notify_type"`
}

// ReconAlertConfig configures reconciliation job alert delivery.
type ReconAlertConfig struct {
	Enabled    bool     `json:"enabled"`
	Recipients []string `json:"recipients"`
	Interval   string   `json:"interval"`
}

type PartitionConfig struct {
	Enabled  bool   `json:"enabled"`
	Interval string `json:"interval"`
}
