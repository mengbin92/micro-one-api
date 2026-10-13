package biz

import (
	"context"
	"fmt"
	"micro-one-api/platform/security/serviceidentity"
	"sort"
	"strings"
	"time"
)

type UpstreamInventory struct {
	SourceKind    string
	SourceID      int64
	ExactModelIDs []string
	AttemptID     string
	ObservedAt    int64
	Manual        bool
}
type InventoryDiff struct{ Added, Missing []string }
type UpstreamInventoryRepo interface {
	SaveUpstreamInventory(context.Context, UpstreamInventory) (InventoryDiff, error)
}

func (uc *ChannelUsecase) RecordUpstreamInventory(ctx context.Context, i UpstreamInventory) (InventoryDiff, error) {
	const method = "/api.channel.v1.ChannelService/RecordUpstreamInventory"
	if !serviceidentity.HasSystemCapability(ctx, method) {
		return InventoryDiff{}, fmt.Errorf("inventory system capability required")
	}
	if i.SourceID <= 0 || (i.SourceKind != "channel" && i.SourceKind != "subscription") || len(i.ExactModelIDs) > 10000 || i.AttemptID == "" || len(i.AttemptID) > 128 || i.ObservedAt <= 0 || i.ObservedAt > time.Now().Add(time.Minute).Unix() {
		return InventoryDiff{}, fmt.Errorf("invalid inventory")
	}
	seen := map[string]bool{}
	for _, id := range i.ExactModelIDs {
		if strings.TrimSpace(id) == "" || len(id) > 255 || strings.ContainsAny(id, "\r\n\x00") || seen[id] {
			return InventoryDiff{}, fmt.Errorf("invalid or duplicate exact model ID")
		}
		seen[id] = true
	}
	sort.Strings(i.ExactModelIDs)
	r, ok := uc.repo.(UpstreamInventoryRepo)
	if !ok {
		return InventoryDiff{}, fmt.Errorf("inventory persistence unavailable")
	}
	return r.SaveUpstreamInventory(ctx, i)
}
