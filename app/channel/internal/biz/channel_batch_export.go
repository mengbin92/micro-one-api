package biz

import (
	"context"
	"fmt"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/events"
	"slices"
	"strings"
)

type ChannelBatchDeleteRepo interface {
	BatchDeleteChannels(context.Context, []int64, map[int64]int64) error
}
type ChannelExportRepo interface {
	ExportChannels(context.Context, int32, int32, string, string, int32, int32) ([]*Channel, int64, error)
}

func (uc *ChannelUsecase) BatchDeleteChannels(ctx context.Context, ids []int64, expected map[int64]int64, reason string) error {
	if len(ids) == 0 || len(ids) > 100 || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("channel IDs (1..100) and reason are required")
	}
	ids = append([]int64(nil), ids...)
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 || (i > 0 && ids[i-1] == id) {
			return fmt.Errorf("invalid or duplicate channel ID")
		}
	}
	ctx = authorization.WithWriteReason(ctx, reason)
	ctx, err := uc.authorize(ctx, "channel.channels.delete", "channel.channel.batch_delete")
	if err != nil {
		return err
	}
	ctx, err = uc.authorizeOptional(ctx, "channel.routing_groups.write", "channel.routing_group.members.update")
	if err != nil {
		return err
	}
	ctx, err = uc.authorizeOptional(ctx, "channel.model_mappings", "channel.model_mapping.delete")
	if err != nil {
		return err
	}
	writer, ok := uc.repo.(ChannelBatchDeleteRepo)
	if !ok {
		return authorization.ErrWriteStorageUnavailable
	}
	if err = writer.BatchDeleteChannels(ctx, ids, expected); err != nil {
		return err
	}
	uc.invalidateModelsListCache()
	for _, id := range ids {
		_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &Channel{ID: id})
	}
	return nil
}
func (uc *ChannelUsecase) ExportChannels(ctx context.Context, page, size int32, keyword, group string, status, kind int32) ([]*Channel, int64, error) {
	ctx, err := uc.authorize(ctx, "channel.channels.export", "channel.channel.export")
	if err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 1000 {
		size = 1000
	}
	reader, ok := uc.repo.(ChannelExportRepo)
	if !ok {
		return nil, 0, authorization.ErrWriteStorageUnavailable
	}
	rows, total, err := reader.ExportChannels(ctx, page, size, keyword, group, status, kind)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*Channel, len(rows))
	for i, row := range rows {
		copy := *row
		copy.Key = ""
		out[i] = &copy
	}
	return out, total, nil
}
