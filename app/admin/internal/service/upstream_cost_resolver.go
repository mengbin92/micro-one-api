package service

import (
	"context"
	"fmt"

	channelv1 "micro-one-api/api/channel/v1"
)

// ── v0.11.0 Phase 2 §2.2: channel-service backed resolution ────────────────
//
// These helpers call channel-service to (a) resolve a source id into a
// human-readable name for the management view, and (b) build the
// channel→model→upstream_model_id table the legacy-key migration tool needs.
// Both are best-effort: a missing channel client or an upstream error returns
// zero values, so the management view and the migration tool degrade
// gracefully instead of failing the whole operation.

// resolveSourceName returns the channel or subscription-account name for the
// given source id. Empty string when the client is missing or the lookup
// fails.
func (s *AdminService) resolveSourceName(ctx context.Context, sourceKind string, sourceID int64) string {
	if s == nil || s.channelClient == nil || sourceID <= 0 {
		return ""
	}
	if sourceKind == "channel" {
		resp, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: sourceID})
		if err != nil || resp == nil || resp.Channel == nil {
			return ""
		}
		return resp.Channel.GetName()
	}
	if sourceKind == "subscription" {
		resp, err := s.channelClient.GetSubscriptionAccount(ctx, &channelv1.GetSubscriptionAccountRequest{AccountId: sourceID})
		if err != nil || resp == nil || resp.Account == nil {
			return ""
		}
		return resp.Account.GetName()
	}
	return ""
}

// upstreamResolver maps channelID -> (canonical public model id -> exact
// upstream model id). It is built once per migration run by paging through
// every channel's model mappings.
type upstreamResolver map[int64]map[string]string

// buildUpstreamResolver loads every channel→model mapping from
// channel-service and indexes them by (channelID, canonical public model id)
// so the legacy-key migration can find the exact upstream_model_id for a
// legacy "<channel_id>:<public_model>" key.
//
// The implementation pages through ListModels (to build a model_pk→model_id
// lookup), then ListChannels + ListChannelModelMappings per channel. N is
// bounded by the channel count (dozens), so the per-channel RPC is acceptable
// and avoids adding a new batch RPC just for the migration tool. When
// channel-service is unreachable the resolver is empty and the migration tool
// falls back to the public model id as the upstream id.
func (s *AdminService) buildUpstreamResolver(ctx context.Context) (upstreamResolver, error) {
	resolver := upstreamResolver{}
	if s == nil || s.channelClient == nil {
		return resolver, nil
	}

	// Build a model_pk → model_id lookup so we can resolve the public model
	// id from the mapping's model_pk. This is essential because the legacy
	// cost key uses the public model_id string (e.g. "gpt-4o"), but the
	// ModelChannelMapping only carries the numeric model_pk. Without this
	// lookup, the migration would silently fall back to the public model id
	// as the upstream id whenever they differ, producing a canonical key that
	// the billing code cannot match (Phase 2 §2.2 bug: zeroed upstream cost).
	modelPKToID := map[int64]string{}
	mPage := int32(1)
	mPageSize := int32(500)
	for {
		mResp, err := s.channelClient.ListModels(ctx, &channelv1.ListModelsRequest{
			Page: mPage, PageSize: mPageSize,
		})
		if err != nil {
			return resolver, fmt.Errorf("list models: %w", err)
		}
		if mResp == nil {
			break
		}
		for _, m := range mResp.GetModels() {
			if m == nil {
				continue
			}
			modelPKToID[m.GetId()] = m.GetModelId()
		}
		if len(mResp.GetModels()) < int(mPageSize) {
			break
		}
		mPage++
	}

	page := int32(1)
	pageSize := int32(200)
	for {
		resp, err := s.channelClient.ListChannels(ctx, &channelv1.ListChannelsRequest{
			Page: page, PageSize: pageSize,
		})
		if err != nil {
			return resolver, fmt.Errorf("list channels: %w", err)
		}
		if resp == nil {
			break
		}
		for _, ch := range resp.GetChannels() {
			if ch == nil {
				continue
			}
			channelID := ch.GetId()
			if channelID <= 0 {
				continue
			}
			mappings, err := s.channelClient.ListChannelModelMappings(ctx, &channelv1.ListChannelModelMappingsRequest{ChannelId: channelID})
			if err != nil || mappings == nil {
				continue
			}
			byModel := map[string]string{}
			for _, m := range mappings.GetMappings() {
				if m == nil {
					continue
				}
				upstream := m.GetUpstreamModelId()
				if upstream == "" {
					continue
				}
				// Index by the public model_id (resolved from model_pk) so
				// the migration can match legacy keys that used the public
				// spelling. Also index by the upstream spelling itself for
				// idempotency when the legacy key already used upstream form.
				if pubID, ok := modelPKToID[m.GetModelPk()]; ok && pubID != "" {
					byModel[canonicalModelID(pubID)] = upstream
				}
				byModel[canonicalModelID(upstream)] = upstream
			}
			if len(byModel) > 0 {
				resolver[channelID] = byModel
			}
		}
		if len(resp.GetChannels()) < int(pageSize) {
			break
		}
		page++
	}
	return resolver, nil
}
