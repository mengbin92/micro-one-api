package biz

import (
	"context"
	"time"
)

// ChannelProbeClient abstracts the channel-service gRPC client used by the
// health checker. By defining this interface in the biz layer, we decouple
// business logic from proto-generated DTOs (the concrete gRPC client is
// injected via a data-layer adapter).
type ChannelProbeClient interface {
	// ListEnabledChannels returns a page of enabled channel summaries.
	ListEnabledChannels(ctx context.Context, page, pageSize int32) ([]ChannelProbeSummary, error)
	// GetChannelDetail returns the full channel info needed for probing.
	GetChannelDetail(ctx context.Context, channelID int64) (*ChannelProbeDetail, error)
	// RecordChannelHealth records the result of a health probe.
	RecordChannelHealth(ctx context.Context, channelID int64, success bool, errMsg string, responseTimeMs int64) error
}

// ChannelProbeSummary is the domain object for a channel listing entry.
type ChannelProbeSummary struct {
	ID     int64
	Type   int32
	Status int32
}

// ChannelProbeDetail is the domain object for a channel's full info needed
// for health probing (base URL, key, API version, type).
type ChannelProbeDetail struct {
	ID         int64
	Type       int32
	BaseURL    string
	Key        string
	APIVersion string
}

// ChannelHealthCheckerConfig holds configuration for the health checker.
type ChannelHealthCheckerConfig struct {
	Enabled  bool
	Interval time.Duration
	Timeout  time.Duration
	PageSize int32
}
