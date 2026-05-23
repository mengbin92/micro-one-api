package config

import (
	"micro-one-api/internal/billing/biz"
	appregistry "micro-one-api/internal/pkg/registry"
)

// Config holds the billing-service configuration.
type Config struct {
	Server   ServerConfig       `json:"server"`
	Data     DataConfig         `json:"data"`
	Billing  BillingConfig      `json:"billing"`
	Payment  biz.PaymentConfig  `json:"payment"`
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

type BillingConfig struct {
	ReservationTimeout string            `json:"reservation_timeout"`
	GroupRatios        map[string]float64 `json:"group_ratios"`
}
