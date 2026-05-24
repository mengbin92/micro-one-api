package config

import appregistry "micro-one-api/internal/pkg/registry"

// Config holds the admin-api configuration.
type Config struct {
	Server   ServerConfig       `json:"server"`
	Clients  ClientsConfig      `json:"clients"`
	Data     DataConfig         `json:"data"`
	Registry appregistry.Config `json:"registry"`
}

// DataConfig holds database configuration for system options storage.
type DataConfig struct {
	Database DatabaseConfig `json:"database"`
}

type DatabaseConfig struct {
	Driver string `json:"driver"`
	Source string `json:"source"`
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

type ClientsConfig struct {
	Identity identityClientConfig `json:"identity"`
	Channel  channelClientConfig  `json:"channel"`
	Billing  billingClientConfig  `json:"billing"`
}

type identityClientConfig struct {
	Endpoint     string `json:"endpoint"`
	HTTPEndpoint string `json:"http_endpoint"`
}

type channelClientConfig struct {
	Endpoint string `json:"endpoint"`
}

type billingClientConfig struct {
	Endpoint string `json:"endpoint"`
}
