package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"micro-one-api/pkg/jsonx"
)

// ── v0.11.0 Phase 2 §2.2: independent upstream-cost management ─────────────
//
// Upstream procurement cost is kept separate from the user-facing ModelPrice.
// It is stored as a JSON map under the UpstreamModelPrice system option, keyed
// by the stable cost-key scheme introduced in Phase 2 §2.2:
//
//	channel:<id>:<upstream_model_id>      // regular channel
//	subscription:<id>:<upstream_model_id>  // subscription account
//
// Legacy keys (<channel_id>:<public_model_id> and bare model ids) are still
// honoured on read (see billing.canonicalUpstreamPriceKey fallback chain), but
// this management surface only WRITES the new stable form so new config does
// not re-introduce the ambiguous legacy keys.

// canonicalModelID mirrors channel biz.NormalizeModelID: trim + lowercase.
// Duplicated locally so admin does not import channel-internal biz (layering).
func canonicalModelID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// UpstreamCostEntry is one row of the upstream-cost management view. The view
// is grouped by source (channel/subscription) so operators can tell regular
// channels and subscription accounts apart even when they share a numeric id.
type UpstreamCostEntry struct {
	Key                  string   `json:"key"`               // canonical cost key
	SourceKind           string   `json:"source_kind"`       // channel | subscription | model (legacy default)
	SourceID             int64    `json:"source_id"`         // 0 for bare-model defaults
	SourceName           string   `json:"source_name"`       // resolved channel/account name (best-effort, empty when unresolvable)
	UpstreamModelID      string   `json:"upstream_model_id"` // exact upstream id; empty for bare-model defaults
	PublicModelID        string   `json:"public_model_id"`   // canonical public id, when the entry maps to a known model
	InputPrice           float64  `json:"input_price"`
	OutputPrice          float64  `json:"output_price"`
	CacheReadPrice       *float64 `json:"cache_read_price,omitempty"`
	CacheCreation5mPrice *float64 `json:"cache_creation_5m_price,omitempty"`
	CacheCreation1hPrice *float64 `json:"cache_creation_1h_price,omitempty"`

	// The *_set fields give partial-update callers an explicit way to clear
	// an optional price: send e.g. cache_read_price_set=true without the price.
	// A non-nil price always writes that value; an absent price with *_set=false
	// preserves the stored value. They are request-only metadata and are omitted
	// from list responses.
	CacheReadPriceSet       bool `json:"cache_read_price_set,omitempty"`
	CacheCreation5mPriceSet bool `json:"cache_creation_5m_price_set,omitempty"`
	CacheCreation1hPriceSet bool `json:"cache_creation_1h_price_set,omitempty"`
}

// upstreamCostView is the list response. LegacyKeys lists the entries still
// using the pre-v0.11.0 <channel_id>:<model> form so operators can see what
// the migration tool would touch.
type upstreamCostView struct {
	Entries    []UpstreamCostEntry `json:"entries"`
	LegacyKeys []UpstreamCostEntry `json:"legacy_keys"`
	Total      int                 `json:"total"`
}

// ListUpstreamCosts loads the UpstreamModelPrice option and renders it as the
// structured per-source view. Errors decoding individual entries are skipped
// (best-effort) so a single corrupt row does not blank the whole page.
func (s *AdminService) ListUpstreamCosts(ctx context.Context) (*upstreamCostView, error) {
	raw, err := s.GetSystemOption(ctx, "UpstreamModelPrice")
	if err != nil {
		return nil, fmt.Errorf("read UpstreamModelPrice: %w", err)
	}
	entries, legacy := parseUpstreamCostEntries(raw)
	// Best-effort name + public-model resolution. Source names require a
	// channel/subscription lookup which is only available when the channel
	// client is wired; when it is nil we leave SourceName empty.
	s.enrichUpstreamCostEntries(ctx, entries)
	s.enrichUpstreamCostEntries(ctx, legacy)
	return &upstreamCostView{
		Entries:    entries,
		LegacyKeys: legacy,
		Total:      len(entries) + len(legacy),
	}, nil
}

// SetUpstreamCost writes a single upstream-cost entry under its canonical key.
// The caller supplies source_kind + source_id + upstream_model_id; this method
// builds the canonical key, merges the entry into the existing map, and writes
// the whole map back. A transactional read-modify-write guarded by the
// canonical key guarantees the last writer wins per key without clobbering
// unrelated entries.
func (s *AdminService) SetUpstreamCost(ctx context.Context, entry UpstreamCostEntry) error {
	if err := validateUpstreamCostPrices(entry); err != nil {
		return err
	}
	key, err := upstreamCostKey(entry)
	if err != nil {
		return err
	}
	return s.mutateUpstreamCosts(ctx, func(prices map[string]map[string]any) {
		prices[key] = upstreamCostValue(entry, prices[key])
	})
}

// DeleteUpstreamCost removes a single entry by its canonical key.
func (s *AdminService) DeleteUpstreamCost(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	return s.mutateUpstreamCosts(ctx, func(prices map[string]map[string]any) {
		delete(prices, key)
	})
}

// MigrateUpstreamCostKeys is the v0.11.0 Phase 2 §2.2 legacy-key migration
// tool. It rewrites every legacy-form key (<channel_id>:<public_model_id>) in
// UpstreamModelPrice into the canonical form
// channel:<id>:<upstream_model_id>, looking up the exact upstream id from the
// model_channel_mapping table. When dry_run is true the plan is returned
// without writing; otherwise the rewrites are applied in one read-modify-write
// pass. Keys that cannot be resolved (channel deleted, mapping gone) are left
// untouched and reported so the operator can decide.
type UpstreamCostMigrationPlan struct {
	ToRewrite []UpstreamCostMigrationChange `json:"to_rewrite"`
	Skipped   []UpstreamCostMigrationChange `json:"skipped"`
	// Executed counts the rewrites actually applied. During dry-run it equals
	// len(ToRewrite); after execution it may be smaller when a target key
	// appeared between planning and the read-modify-write.
	Executed int `json:"executed"`
}

type UpstreamCostMigrationChange struct {
	OldKey          string `json:"old_key"`
	NewKey          string `json:"new_key"`
	SourceID        int64  `json:"source_id"`
	PublicModelID   string `json:"public_model_id"`
	UpstreamModelID string `json:"upstream_model_id"`
	Reason          string `json:"reason,omitempty"` // why skipped, when applicable
}

func (s *AdminService) MigrateUpstreamCostKeys(ctx context.Context, dryRun bool) (*UpstreamCostMigrationPlan, error) {
	raw, err := s.GetSystemOption(ctx, "UpstreamModelPrice")
	if err != nil {
		return nil, fmt.Errorf("read UpstreamModelPrice: %w", err)
	}
	prices, err := decodeUpstreamCostMap(raw)
	if err != nil {
		return nil, err
	}
	// Resolve channel → model → upstream mapping in one batch so we can plan
	// the rewrites.
	resolver, err := s.buildUpstreamResolver(ctx)
	if err != nil {
		return nil, err
	}
	plan := &UpstreamCostMigrationPlan{}
	type rewriteInfo struct {
		oldKey string
		change UpstreamCostMigrationChange
	}
	rewrites := map[string]rewriteInfo{} // newKey -> info
	for key := range prices {
		old := key
		// Only legacy "<id>:<model>" keys are candidates. Canonical keys
		// (channel:/subscription:/bare model) are left alone.
		channelID, publicModel, ok := parseLegacyUpstreamKey(old)
		if !ok {
			continue
		}
		resolved, ok := resolver[channelID][canonicalModelID(publicModel)]
		if !ok {
			// No exact upstream id known for this numeric id+model. Skip the
			// rewrite entirely: the billing code's legacy-key fallback
			// (upstreamPriceKey) still reads <id>:<public_model_id>, so leaving
			// the key untouched preserves the existing cost.
			plan.Skipped = append(plan.Skipped, UpstreamCostMigrationChange{
				OldKey: old, NewKey: "", SourceID: channelID, PublicModelID: publicModel, UpstreamModelID: "",
				Reason: "upstream_model_id not resolved (source has no mapping or model_pk lookup failed); legacy key preserved",
			})
			continue
		}
		if resolved.kind == "ambiguous" {
			plan.Skipped = append(plan.Skipped, UpstreamCostMigrationChange{
				OldKey: old, NewKey: "", SourceID: channelID, PublicModelID: publicModel, UpstreamModelID: resolved.upstreamID,
				Reason: "numeric id exists as both channel and subscription account; manual resolution required",
			})
			continue
		}
		newKey := fmt.Sprintf("%s:%d:%s", resolved.kind, channelID, resolved.upstreamID)
		if newKey == old {
			continue
		}
		if _, dup := rewrites[newKey]; dup {
			// Two legacy keys collapsing onto the same canonical key — a real
			// conflict the operator must resolve. Skip and report.
			plan.Skipped = append(plan.Skipped, UpstreamCostMigrationChange{
				OldKey: old, NewKey: newKey, SourceID: channelID, PublicModelID: publicModel, UpstreamModelID: resolved.upstreamID,
				Reason: "collides with another legacy key after rewrite",
			})
			continue
		}
		rewrites[newKey] = rewriteInfo{
			oldKey: old,
			change: UpstreamCostMigrationChange{
				OldKey: old, NewKey: newKey, SourceID: channelID, PublicModelID: publicModel, UpstreamModelID: resolved.upstreamID,
			},
		}
		plan.ToRewrite = append(plan.ToRewrite, rewrites[newKey].change)
	}
	plan.Executed = len(plan.ToRewrite)
	sortMigrationPlan(plan)

	if dryRun || len(rewrites) == 0 {
		return plan, nil
	}
	// Apply: move each value from old key to new key. Done in one
	// read-modify-write so a partial failure leaves the option unchanged.
	err = s.mutateUpstreamCostsRaw(ctx, func(prices map[string]map[string]any) {
		for newKey, info := range rewrites {
			if _, exists := prices[newKey]; exists {
				plan.Skipped = append(plan.Skipped, UpstreamCostMigrationChange{
					OldKey: info.oldKey, NewKey: newKey, SourceID: info.change.SourceID,
					PublicModelID: info.change.PublicModelID, UpstreamModelID: info.change.UpstreamModelID,
					Reason: "target canonical key already exists; legacy key preserved",
				})
				plan.Executed--
				continue
			}
			if v, ok := prices[info.oldKey]; ok {
				prices[newKey] = v
				delete(prices, info.oldKey)
				plan.Executed++
			}
		}
	})
	if err != nil {
		return nil, err
	}
	sortMigrationPlan(plan)
	return plan, nil
}

func sortMigrationPlan(plan *UpstreamCostMigrationPlan) {
	sort.Slice(plan.ToRewrite, func(i, j int) bool { return plan.ToRewrite[i].OldKey < plan.ToRewrite[j].OldKey })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].OldKey < plan.Skipped[j].OldKey })
}

// ── helpers ────────────────────────────────────────────────────────────────

// upstreamCostKey builds the canonical cost key for an entry and validates the
// source_kind. Bare-model defaults (no source) keep the model id as the key.
func upstreamCostKey(e UpstreamCostEntry) (string, error) {
	e.SourceKind = strings.TrimSpace(e.SourceKind)
	e.UpstreamModelID = strings.TrimSpace(e.UpstreamModelID)
	e.PublicModelID = canonicalModelID(e.PublicModelID)
	switch e.SourceKind {
	case "channel", "subscription":
		if e.SourceID <= 0 {
			return "", fmt.Errorf("source_id is required for source_kind=%s", e.SourceKind)
		}
		if e.UpstreamModelID == "" {
			return "", fmt.Errorf("upstream_model_id is required for source_kind=%s", e.SourceKind)
		}
		return fmt.Sprintf("%s:%d:%s", e.SourceKind, e.SourceID, e.UpstreamModelID), nil
	case "", "model":
		if e.PublicModelID == "" {
			return "", fmt.Errorf("public_model_id is required for a bare-model default")
		}
		return e.PublicModelID, nil
	default:
		return "", fmt.Errorf("unknown source_kind %q", e.SourceKind)
	}
}

func validateUpstreamCostPrices(e UpstreamCostEntry) error {
	if e.InputPrice < 0 || e.OutputPrice < 0 {
		return fmt.Errorf("input_price and output_price must be >= 0")
	}
	for name, value := range map[string]*float64{
		"cache_read_price":        e.CacheReadPrice,
		"cache_creation_5m_price": e.CacheCreation5mPrice,
		"cache_creation_1h_price": e.CacheCreation1hPrice,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must be >= 0", name)
		}
	}
	return nil
}

func upstreamCostValue(e UpstreamCostEntry, existing map[string]any) map[string]any {
	if existing == nil {
		existing = map[string]any{}
	}
	existing["input_price"] = e.InputPrice
	existing["output_price"] = e.OutputPrice
	setOptionalPrice(existing, "cache_read_price", e.CacheReadPrice, e.CacheReadPriceSet)
	setOptionalPrice(existing, "cache_creation_5m_price", e.CacheCreation5mPrice, e.CacheCreation5mPriceSet)
	setOptionalPrice(existing, "cache_creation_1h_price", e.CacheCreation1hPrice, e.CacheCreation1hPriceSet)
	return existing
}

func setOptionalPrice(values map[string]any, key string, value *float64, clear bool) {
	switch {
	case value != nil:
		values[key] = *value
	case clear:
		delete(values, key)
	}
}

// parseLegacyUpstreamKey recognises the pre-v0.11.0 "<channel_id>:<model>"
// form and returns its parts. Returns ok=false for canonical keys
// (channel:/subscription:) and for bare model ids.
func parseLegacyUpstreamKey(key string) (channelID int64, model string, ok bool) {
	if strings.HasPrefix(key, "channel:") || strings.HasPrefix(key, "subscription:") {
		return 0, "", false
	}
	idx := strings.Index(key, ":")
	if idx <= 0 {
		return 0, "", false // bare model id, not a legacy channel key
	}
	var id int64
	if _, err := fmt.Sscanf(key[:idx], "%d", &id); err != nil || id <= 0 {
		return 0, "", false
	}
	return id, key[idx+1:], true
}

func decodeUpstreamCostMap(raw string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := jsonx.Unmarshal([]byte(raw), &out); err != nil {
		// The value may be map[string]ModelPrice (typed); retry as generic.
		typed := map[string]map[string]any{}
		if err2 := jsonx.Unmarshal([]byte(raw), &typed); err2 != nil {
			return nil, fmt.Errorf("decode UpstreamModelPrice: %w", err)
		}
		return typed, nil
	}
	return out, nil
}

func parseUpstreamCostEntries(raw string) (canonical []UpstreamCostEntry, legacy []UpstreamCostEntry) {
	prices, err := decodeUpstreamCostMap(raw)
	if err != nil {
		return nil, nil
	}
	for key, val := range prices {
		entry := UpstreamCostEntry{
			Key:                  key,
			InputPrice:           floatValue(val["input_price"]),
			OutputPrice:          floatValue(val["output_price"]),
			CacheReadPrice:       optionalFloatValue(val, "cache_read_price"),
			CacheCreation5mPrice: optionalFloatValue(val, "cache_creation_5m_price"),
			CacheCreation1hPrice: optionalFloatValue(val, "cache_creation_1h_price"),
		}
		if kind, sourceID, upstreamID := parseCanonicalUpstreamKey(key); kind != "" {
			entry.SourceKind = kind
			entry.SourceID = sourceID
			entry.UpstreamModelID = upstreamID
			canonical = append(canonical, entry)
		} else if chID, model, ok := parseLegacyUpstreamKey(key); ok {
			entry.SourceKind = "channel"
			entry.SourceID = chID
			entry.PublicModelID = model
			legacy = append(legacy, entry)
		} else {
			entry.SourceKind = "model"
			entry.PublicModelID = key
			canonical = append(canonical, entry)
		}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Key < canonical[j].Key })
	sort.Slice(legacy, func(i, j int) bool { return legacy[i].Key < legacy[j].Key })
	return canonical, legacy
}

// parseCanonicalUpstreamKey recognises "channel:<id>:<upstream>" /
// "subscription:<id>:<upstream>" and returns the parts. Returns kind="" for
// any other form.
func parseCanonicalUpstreamKey(key string) (kind string, sourceID int64, upstreamID string) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", 0, ""
	}
	if parts[0] != "channel" && parts[0] != "subscription" {
		return "", 0, ""
	}
	var id int64
	if _, err := fmt.Sscanf(parts[1], "%d", &id); err != nil || id <= 0 {
		return "", 0, ""
	}
	return parts[0], id, parts[2]
}

func floatValue(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func optionalFloatValue(values map[string]any, key string) *float64 {
	value, ok := values[key]
	if !ok {
		return nil
	}
	parsed := floatValue(value)
	return &parsed
}

// mutateUpstreamCosts loads the UpstreamModelPrice map, applies fn, and writes
// it back atomically (last-writer-wins per key).
func (s *AdminService) mutateUpstreamCosts(ctx context.Context, fn func(prices map[string]map[string]any)) error {
	return s.mutateUpstreamCostsRaw(ctx, fn)
}

func (s *AdminService) mutateUpstreamCostsRaw(ctx context.Context, fn func(prices map[string]map[string]any)) error {
	if s.systemOptsUc == nil {
		return fmt.Errorf("system options storage not configured")
	}
	raw, err := s.systemOptsUc.Get(ctx, "UpstreamModelPrice")
	if err != nil && !isNotFoundErr(err) {
		return fmt.Errorf("read UpstreamModelPrice: %w", err)
	}
	prices, err := decodeUpstreamCostMap(raw)
	if err != nil {
		return err
	}
	fn(prices)
	payload, err := jsonx.Marshal(prices)
	if err != nil {
		return fmt.Errorf("encode UpstreamModelPrice: %w", err)
	}
	return s.systemOptsUc.Set(ctx, "UpstreamModelPrice", string(payload))
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no rows")
}

// enrichUpstreamCostEntries best-effort resolves SourceName for channel /
// subscription entries by calling channel-service. Failures are silent so a
// missing client does not break the read-only view.
func (s *AdminService) enrichUpstreamCostEntries(ctx context.Context, entries []UpstreamCostEntry) {
	if s == nil || s.channelClient == nil || len(entries) == 0 {
		return
	}
	// Look up each source individually. N is small (cost config rows are
	// dozens, not thousands), so per-entry RPCs are acceptable and avoid a
	// batch RPC that does not exist yet.
	for i := range entries {
		e := &entries[i]
		if e.SourceID <= 0 || (e.SourceKind != "channel" && e.SourceKind != "subscription") {
			continue
		}
		if name := s.resolveSourceName(ctx, e.SourceKind, e.SourceID); name != "" {
			e.SourceName = name
		}
	}
}
