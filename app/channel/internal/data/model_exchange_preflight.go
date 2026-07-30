package data

import (
	"context"
	"fmt"
	"strings"

	"micro-one-api/app/channel/internal/biz"
)

// ── Unified import preflight (code review #4) ──────────────────────────────
//
// preflightImport validates an import document BEFORE any write (dry-run or
// real). It catches:
//   - alias conflicts (alias belongs to another model, or duplicate alias
//     within the document)
//   - channel_id == 0 in a channel mapping
//   - subscription_account_id == 0 in a subscription mapping
//   - duplicate mappings within the same model (same channel_id or same
//     account+group)
//   - dangling foreign keys: channel_id / subscription_account_id that do not
//     exist in the target environment
//
// Both dry-run and the real import call preflightImport first so their
// validation is identical (roadmap acceptance: "dry-run and real import use
// the same validator"). On any violation it returns a typed biz error so the
// caller can classify it.

// preflightImport validates the import document and returns an error listing
// all violations. It is called before dry-run classification and before the
// real write transaction. existingAliases maps normalized alias -> model PK
// for aliases that already exist in the target environment. existingModelPKs
// maps canonical model_id -> model PK so the validator can tell whether an
// alias belongs to the SAME model being updated (not a conflict) or to a
// DIFFERENT model (real conflict) (code review HIGH-3).
func (r *Repository) preflightImport(ctx context.Context, models []*biz.ModelExportModel, existingAliases map[string]int64, existingModelPKs map[string]int64) error {
	var errs []string

	// Track model_ids within this document to catch duplicate model_id.
	docModelIDs := make(map[string]bool)

	// Collect channel and subscription account IDs referenced by mappings for
	// FK validation.
	channelIDs := make(map[int64]bool)
	accountIDs := make(map[int64]bool)

	// Phase 1: structural validation and collect document-level alias ownership.
	// last-writer-wins; claimants records every model that lists the alias.
	docAliasOwner := make(map[string]string)       // normalized alias -> canonical model_id
	docAliasClaimants := make(map[string][]string) // normalized alias -> canonical model_ids
	for _, em := range models {
		if em == nil {
			continue
		}
		canonicalID := biz.NormalizeModelID(em.ModelID)
		if canonicalID == "" {
			errs = append(errs, "record with empty model_id")
			continue
		}
		if docModelIDs[canonicalID] {
			errs = append(errs, fmt.Sprintf("duplicate model_id %s in document", em.ModelID))
		}
		docModelIDs[canonicalID] = true

		seenAliasInModel := make(map[string]bool)
		for _, a := range em.Aliases {
			if a == nil || strings.TrimSpace(a.Alias) == "" {
				continue
			}
			normalized := biz.NormalizeModelID(a.Alias)
			if seenAliasInModel[normalized] {
				errs = append(errs, fmt.Sprintf("model %s: duplicate alias %s", em.ModelID, normalized))
				continue
			}
			seenAliasInModel[normalized] = true
			docAliasOwner[normalized] = canonicalID
			docAliasClaimants[normalized] = append(docAliasClaimants[normalized], canonicalID)
		}

		// Validate channel mappings.
		seenChannelMapping := make(map[int64]bool)
		for _, m := range em.ChannelMappings {
			if m == nil {
				continue
			}
			if m.ChannelID == 0 {
				errs = append(errs, fmt.Sprintf("model %s: channel mapping has no channel_id", em.ModelID))
				continue
			}
			if seenChannelMapping[m.ChannelID] {
				errs = append(errs, fmt.Sprintf("model %s: duplicate channel mapping for channel %d", em.ModelID, m.ChannelID))
			}
			seenChannelMapping[m.ChannelID] = true
			channelIDs[m.ChannelID] = true
		}

		// Validate subscription mappings.
		seenSubMapping := make(map[string]bool)
		for _, m := range em.SubscriptionMappings {
			if m == nil {
				continue
			}
			if m.SubscriptionAccountID == 0 {
				errs = append(errs, fmt.Sprintf("model %s: subscription mapping has no subscription_account_id", em.ModelID))
				continue
			}
			group := m.GroupName
			if group == "" {
				group = "default"
			}
			key := fmt.Sprintf("%d|%s", m.SubscriptionAccountID, group)
			if seenSubMapping[key] {
				errs = append(errs, fmt.Sprintf("model %s: duplicate subscription mapping for account %d group %s", em.ModelID, m.SubscriptionAccountID, group))
			}
			seenSubMapping[key] = true
			accountIDs[m.SubscriptionAccountID] = true
		}
	}

	// Phase 2: alias conflict detection against the target environment.
	// A conflict only occurs when:
	//   - the same alias is claimed by more than one model in this document; or
	//   - the DB owner of the alias is a different model AND that DB owner still
	//     lists the alias in this document (i.e. the alias is not being moved).
	for normalized, claimants := range docAliasClaimants {
		if len(claimants) > 1 {
			errs = append(errs, fmt.Sprintf("alias %s claimed by multiple models in document: %s", normalized, strings.Join(claimants, ", ")))
			continue
		}
		finalOwner := docAliasOwner[normalized]
		dbOwnerPK, hasDBOwner := existingAliases[normalized]
		if !hasDBOwner || dbOwnerPK <= 0 {
			continue
		}
		finalOwnerPK := existingModelPKs[finalOwner]
		if dbOwnerPK == finalOwnerPK {
			continue
		}
		// The DB owner differs. Is the DB owner dropping the alias in this doc?
		// If the DB owner is not present in the document at all we cannot know
		// whether it keeps the alias elsewhere, so treat it as a conflict.
		dbOwnerStillClaims := true
		dbOwnerPresentInDoc := false
		for _, em := range models {
			if em == nil {
				continue
			}
			if existingModelPKs[biz.NormalizeModelID(em.ModelID)] != dbOwnerPK {
				continue
			}
			dbOwnerPresentInDoc = true
			for _, a := range em.Aliases {
				if a != nil && biz.NormalizeModelID(a.Alias) == normalized {
					dbOwnerStillClaims = true
					break
				}
			}
			// If the DB owner is present but does not list the alias, it is
			// explicitly dropping it -> allow the move.
			dbOwnerStillClaims = false
			break
		}
		if !dbOwnerPresentInDoc || dbOwnerStillClaims {
			errs = append(errs, fmt.Sprintf("model %s: alias %s already belongs to model PK %d", finalOwner, normalized, dbOwnerPK))
		}
	}

	// FK validation: verify referenced channels and accounts exist.
	// Both DB and memory modes validate FK targets so dry-run and real import
	// share identical validation semantics (code review MEDIUM-1).
	if len(channelIDs) > 0 {
		ids := mapKeysToInt64Slice(channelIDs)
		existing, err := r.batchChannelIDsExist(ctx, ids)
		if err != nil {
			errs = append(errs, fmt.Sprintf("preflight: failed to validate channel ids: %v", err))
		} else {
			for _, id := range ids {
				if !existing[id] {
					errs = append(errs, fmt.Sprintf("channel_id %d does not exist (dangling foreign key)", id))
				}
			}
		}
	}
	if len(accountIDs) > 0 {
		ids := mapKeysToInt64Slice(accountIDs)
		existing, err := r.batchSubscriptionAccountIDsExist(ctx, ids)
		if err != nil {
			errs = append(errs, fmt.Sprintf("preflight: failed to validate subscription account ids: %v", err))
		} else {
			for _, id := range ids {
				if !existing[id] {
					errs = append(errs, fmt.Sprintf("subscription_account_id %d does not exist (dangling foreign key)", id))
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %d validation error(s): %s", biz.ErrImportInvalidRecord, len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// loadExistingAliasOwners loads all aliases and returns a map of normalized
// alias -> model PK. Used by preflight to detect alias conflicts against the
// existing registry.
func (r *Repository) loadExistingAliasOwners(ctx context.Context) (map[string]int64, error) {
	if r.db == nil {
		// Memory store.
		out := make(map[string]int64)
		for _, a := range r.modelAliases {
			if a != nil {
				out[biz.NormalizeModelID(a.Alias)] = a.ModelPK
			}
		}
		return out, nil
	}
	var pos []modelAliasModel
	if err := r.db.WithContext(ctx).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(pos))
	for i := range pos {
		out[biz.NormalizeModelID(pos[i].Alias)] = pos[i].ModelPK
	}
	return out, nil
}

// loadExistingModelPKs returns a map of canonical model_id -> model PK for
// models that already exist in the target environment. Used by preflight to
// distinguish same-owner aliases (the alias belongs to the model being updated)
// from cross-model conflicts (code review HIGH-3).
func (r *Repository) loadExistingModelPKs(ctx context.Context, models []*biz.ModelExportModel) (map[string]int64, error) {
	if r.db == nil {
		out := make(map[string]int64)
		for _, m := range r.models {
			if m != nil {
				out[biz.NormalizeModelID(m.ModelID)] = m.ID
			}
		}
		return out, nil
	}
	existing, err := r.loadExistingModelsByModelID(ctx, models)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(existing))
	for canonical, m := range existing {
		if m != nil {
			out[canonical] = m.ID
		}
	}
	return out, nil
}

// batchChannelIDsExist checks which channel IDs exist. In DB mode it queries the
// channels table; in memory mode it checks the in-memory channel store.
func (r *Repository) batchChannelIDsExist(ctx context.Context, ids []int64) (map[int64]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if r.db == nil {
		// Memory mode: check the in-memory channel store.
		out := make(map[int64]bool, len(ids))
		for _, ch := range r.channels {
			if ch != nil {
				out[ch.ID] = true
			}
		}
		return out, nil
	}
	var found []int64
	if err := r.db.WithContext(ctx).Model(&channelModel{}).Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// batchSubscriptionAccountIDsExist checks which subscription account IDs exist.
// In DB mode it queries the subscription_accounts table; in memory mode it
// checks the in-memory store.
func (r *Repository) batchSubscriptionAccountIDsExist(ctx context.Context, ids []int64) (map[int64]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if r.db == nil {
		// Memory mode: check the in-memory subscription account store.
		out := make(map[int64]bool, len(ids))
		for _, acct := range r.subAccounts {
			if acct != nil {
				out[acct.ID] = true
			}
		}
		return out, nil
	}
	var found []int64
	if err := r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

func mapKeysToInt64Slice(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
