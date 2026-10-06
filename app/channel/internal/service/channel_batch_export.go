package service

import (
	"bytes"
	"context"
	"encoding/csv"
	channelv1 "micro-one-api/api/channel/v1"
	"net/url"
	"strconv"
	"strings"
)

func (s *ChannelService) BatchDeleteChannels(ctx context.Context, req *channelv1.BatchDeleteChannelsRequest) (*channelv1.BatchDeleteChannelsResponse, error) {
	if err := s.uc.BatchDeleteChannels(ctx, req.ChannelIds, req.ExpectedRevisions, req.Reason); err != nil {
		return nil, err
	}
	return &channelv1.BatchDeleteChannelsResponse{Success: true, DeletedChannelIds: req.ChannelIds}, nil
}
func (s *ChannelService) ExportChannels(ctx context.Context, req *channelv1.ListChannelsRequest) (*channelv1.ChannelExportArtifact, error) {
	rows, _, err := s.uc.ExportChannels(ctx, req.Page, req.PageSize, req.Keyword, req.Group, req.Status, req.Type)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Write([]string{"id", "name", "type", "status", "base_url", "models", "group", "priority", "weight", "revision"})
	for _, channel := range rows {
		base := channel.BaseURL
		if parsed, parseErr := url.Parse(base); parseErr == nil {
			parsed.User = nil
			parsed.RawQuery = ""
			parsed.Fragment = ""
			base = parsed.String()
		}
		writer.Write([]string{strconv.FormatInt(channel.ID, 10), channelCSVText(channel.Name), strconv.FormatInt(int64(channel.Type), 10), strconv.FormatInt(int64(channel.Status), 10), channelCSVText(base), channelCSVText(strings.Join(channel.Models, ",")), channelCSVText(channel.Group), strconv.FormatInt(channel.Priority, 10), strconv.FormatUint(uint64(channel.Weight), 10), strconv.FormatInt(channel.AuthorizationRevision, 10)})
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, err
	}
	return &channelv1.ChannelExportArtifact{ContentType: "text/csv; charset=utf-8", FileName: "channels.csv", Body: buf.Bytes()}, nil
}
func channelCSVText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsAny(trimmed[:1], "=+-@") {
		return "'" + value
	}
	return value
}
