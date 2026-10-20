package data

import (
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/pkg/jsonx"
)

// Allocate final log IDs while claiming keys. Claim -> log -> refs keeps the
// legacy writer's lock order. RETURNING distinguishes fresh claims atomically,
// avoiding a growing IN-list scan and a second claim-row version per summary.
func createTimingPostgresBatch(tx *gorm.DB, keys []string, entries map[string]*biz.LogEntry, ids map[string]int64) error {
	encoded, err := jsonx.Marshal(keys)
	if err != nil {
		return err
	}
	fresh, err := reserveTimingPostgresClaims(tx, string(encoded))
	if err != nil {
		return err
	}
	var replay []string
	var models []logModel
	var refs []requestTraceAttemptRefModel
	for _, key := range keys {
		id, owned := fresh[key]
		if !owned {
			replay = append(replay, key)
			continue
		}
		model, err := newLog(entries[key])
		if err != nil {
			return err
		}
		model.ID = id
		models = append(models, *model)
		refs = append(refs, newRequestTraceAttemptRefs(entries[key])...)
		ids[key] = id
	}
	if len(replay) > 0 {
		var claims []logIngestDedupeClaimModel
		if err := tx.Where("dedupe_key IN ?", replay).Find(&claims).Error; err != nil {
			return err
		}
		for _, claim := range claims {
			ids[claim.DedupeKey] = claim.LogID
		}
		for _, key := range replay {
			if ids[key] <= 0 {
				return biz.ErrInvalidRequestTiming
			}
		}
	}
	if len(models) == 0 {
		return nil
	}
	if err := createTimingLogModels(tx, models); err != nil {
		return err
	}
	if len(refs) > 0 {
		return tx.CreateInBatches(&refs, timingRefInsertBatchSize).Error
	}
	return nil
}

func reserveTimingPostgresClaims(tx *gorm.DB, encodedKeys string) (map[string]int64, error) {
	const query = `INSERT INTO "log_ingest_dedupe_claims" ("dedupe_key", "log_id", "created_at")
SELECT incoming.dedupe_key, nextval(pg_get_serial_sequence($2, 'id')::regclass), $3::bigint
FROM jsonb_array_elements_text($1::jsonb) AS incoming(dedupe_key)
ORDER BY incoming.dedupe_key
ON CONFLICT ("dedupe_key") DO NOTHING
RETURNING "dedupe_key", "log_id"`
	rows, err := tx.Statement.ConnPool.QueryContext(tx.Statement.Context, query, encodedKeys, (logModel{}).TableName(), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fresh := make(map[string]int64)
	for rows.Next() {
		var key string
		var id int64
		if err := rows.Scan(&key, &id); err != nil {
			return nil, err
		}
		fresh[key] = id
	}
	return fresh, rows.Err()
}
