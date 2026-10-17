package data

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

type executionOutboxModel struct {
	ObservationID string `gorm:"column:observation_id"`
	TraceParent   string `gorm:"column:trace_parent"`
	TraceState    string `gorm:"column:trace_state"`
	AcceptedAtMS  int64  `gorm:"column:telemetry_accepted_at_ms"`
	QueuedAtMS    int64  `gorm:"column:telemetry_queued_at_ms"`

	ID               string `gorm:"primaryKey"`
	Digest           string
	Payload          string
	Status           string `gorm:"index"`
	NextRetry        int64  `gorm:"index"`
	CreatedAt        int64
	AcknowledgedAt   int64  `gorm:"not null;default:0"`
	IntentDigest     string `gorm:"not null;default:''"`
	EvidenceDeadline int64  `gorm:"not null;default:0"`
	HeaderReserved   int64  `gorm:"not null;default:0"`
	TerminalReserved int64  `gorm:"not null;default:0"`
	FinalReserved    int64  `gorm:"not null;default:0"`
}

func (executionOutboxModel) TableName() string { return "execution_outbox" }

type executionOutboxRepo struct {
	d           *Data
	path        string
	diskReserve int64
}

const (
	outboxMaxEvents = 10000
	outboxMaxBytes  = 120 << 20
	outboxDBBytes   = 128 << 20
)

func NewExecutionData(path string, client biz.ExecutionSender) (*Data, func(), error) {
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("outbox path must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	// #nosec G302 -- This is a private directory; owner execute permission is required to access its 0600 database.
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, nil, err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, nil, fmt.Errorf("outbox must be a regular file")
	}
	// #nosec G304 -- The absolute path is trusted server configuration, not request input; existing symlinks are rejected above.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	_ = f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, nil, err
	}
	dsn := url.URL{Scheme: "file", Path: path}
	q := url.Values{"_journal_mode": {"WAL"}, "_synchronous": {"FULL"}, "_busy_timeout": {"5000"}}
	dsn.RawQuery = q.Encode()
	db, err := gorm.Open(sqlite.Open(dsn.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, nil, err
	}
	sql, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	sql.SetMaxOpenConns(1)
	cleanup := func() { _ = sql.Close() }
	var pageSize int64
	if err = db.Raw("PRAGMA page_size").Scan(&pageSize).Error; err != nil || pageSize <= 0 {
		cleanup()
		return nil, nil, fmt.Errorf("outbox page size unavailable")
	}
	for _, query := range []string{fmt.Sprintf("PRAGMA max_page_count=%d", outboxDBBytes/pageSize), "PRAGMA journal_size_limit=1048576", fmt.Sprintf("PRAGMA wal_autocheckpoint=%d", max(int64(1), (1<<20)/pageSize))} {
		if err = db.Exec(query).Error; err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	if err = db.AutoMigrate(&executionOutboxModel{}); err != nil {
		cleanup()
		return nil, nil, err
	}
	// Old schemas did not record receipt time. Start their retention clock at
	// upgrade rather than immediately deleting delayed deliveries by creation time.
	if err = db.Model(&executionOutboxModel{}).Where("status = ? AND acknowledged_at = 0", "acknowledged").Update("acknowledged_at", time.Now().Unix()).Error; err != nil {
		cleanup()
		return nil, nil, err
	}
	if err = restoreOutboxReservations(db); err != nil {
		cleanup()
		return nil, nil, err
	}
	return &Data{ExecutionDB: db, ExecutionSender: client}, cleanup, nil
}
func NewExecutionOutboxRepo(d *Data, path string) biz.ExecutionOutboxRepo {
	// An event can grow the database and its WAL before checkpointing. Include
	// full legal payload size twice, plus page/index/transaction overhead in both
	// files. Fall back to SQLite's largest page size if inspection fails.
	pageSize := int64(65536)
	var actual int64
	if err := d.ExecutionDB.Raw("PRAGMA page_size").Scan(&actual).Error; err == nil && actual > 0 {
		pageSize = actual
	}
	return &executionOutboxRepo{d: d, path: path, diskReserve: 2 * (execution.MaxEventBytes + 32*pageSize)}
}
func (r *executionOutboxRepo) Put(ctx context.Context, e execution.Event) error {
	return r.put(ctx, e, false)
}
func (r *executionOutboxRepo) Admit(ctx context.Context, e execution.Event) error {
	return r.put(ctx, e, true)
}
func (r *executionOutboxRepo) put(ctx context.Context, e execution.Event, admit bool) error {
	b, d, err := e.Encode()
	if err != nil {
		return err
	}
	if admit && e.Category != "intent" {
		return fmt.Errorf("outbox admission requires an intent")
	}
	intentDigest, err := outboxIntentDigest(e.Intent)
	if err != nil {
		return err
	}
	if err := r.expireReservations(ctx); err != nil {
		return err
	}
	return r.d.ExecutionDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old executionOutboxModel
		if err := tx.Where("id = ?", e.ID).Limit(1).Find(&old).Error; err != nil {
			return err
		}
		if old.ID != "" {
			if admit {
				return biz.ErrExecutionAlreadyAdmitted
			}
			if old.Digest != d {
				return fmt.Errorf("outbox payload conflict")
			}
			return nil
		}
		now := time.Now().Unix()
		// Bound cleanup writes so retention maintenance cannot fill the WAL in a
		// single admission. Never prune an intent that still protects completion.
		oldestACK := tx.Model(&executionOutboxModel{}).Select("id").Where("status = ? AND acknowledged_at > 0 AND acknowledged_at < ? AND header_reserved = 0 AND terminal_reserved = 0 AND final_reserved = 0", "acknowledged", time.Now().Add(-7*24*time.Hour).Unix()).Order("acknowledged_at ASC").Limit(1)
		if err := tx.Where("id IN (?)", oldestACK).Delete(&executionOutboxModel{}).Error; err != nil {
			return err
		}
		var capacity struct {
			Pending  int64
			Reserved int64
		}
		if err := tx.Model(&executionOutboxModel{}).Select("COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END),0) AS pending, COALESCE(SUM(header_reserved + terminal_reserved + final_reserved),0) AS reserved").Scan(&capacity).Error; err != nil {
			return err
		}
		var bytes int64
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if info, err := os.Stat(r.path + suffix); err == nil {
				bytes += info.Size()
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		telemetry := requesttrace.Capture(ctx)
		row := executionOutboxModel{ObservationID: telemetry.ObservationID, TraceParent: telemetry.TraceParent, TraceState: telemetry.TraceState, AcceptedAtMS: telemetry.AcceptedAtMS, QueuedAtMS: telemetry.QueuedAtMS, ID: e.ID, Digest: d, Payload: string(b), Status: "pending", CreatedAt: now}
		addedSlots := int64(1)
		writeSlots := int64(1)
		var origin executionOutboxModel
		consume := ""
		if e.Category == "intent" {
			row.IntentDigest, row.EvidenceDeadline = intentDigest, e.Intent.EvidenceDeadline
			if row.EvidenceDeadline > now {
				row.HeaderReserved = 1
				row.TerminalReserved = 1
				if e.Intent.Purpose == "user" {
					row.FinalReserved = 1
				}
			}
			addedSlots += row.HeaderReserved + row.TerminalReserved + row.FinalReserved
			writeSlots = addedSlots
		} else {
			if err := tx.Where("id = ?", e.Intent.Key()+"/intent/0").Limit(1).Find(&origin).Error; err != nil {
				return err
			}
			if origin.ID == "" || origin.IntentDigest != "" && origin.IntentDigest != intentDigest {
				return fmt.Errorf("outbox intent missing or changed")
			}
			if e.Category == "checkpoint" && e.Record != nil && e.Record.Execution == "checkpoint" && e.Record.Observation.Reason == "response_headers_only" && origin.HeaderReserved > 0 {
				consume = "header_reserved"
			} else if e.Category == "terminal" && origin.TerminalReserved > 0 {
				consume = "terminal_reserved"
			} else if e.Category == "final" && origin.FinalReserved > 0 {
				consume = "final_reserved"
			}
			if consume != "" {
				// Existing capacity becomes a real pending row in the same transaction.
				addedSlots, writeSlots = 0, 0
			}
		}
		if consume != "" {
			// Earlier schemas could already admit more future evidence than fits.
			// Block new promises, but let protected completions make progress within
			// actual remaining capacity instead of deadlocking on that legacy debt.
			if capacity.Pending+1 > outboxMaxEvents || bytes+r.diskReserve > outboxMaxBytes {
				return fmt.Errorf("outbox capacity exhausted")
			}
			if err := tx.Model(&executionOutboxModel{}).Where("id = ?", origin.ID).Update(consume, 0).Error; err != nil {
				return err
			}
		} else if capacity.Pending+capacity.Reserved+addedSlots > outboxMaxEvents || bytes+capacity.Reserved*r.diskReserve+writeSlots*r.diskReserve > outboxMaxBytes {
			return fmt.Errorf("outbox capacity exhausted")
		}
		return tx.Create(&row).Error
	})
}

// Expiry releases only capacity, retaining events and digest tombstones for
// late supplier evidence. Commit small maintenance batches independently so a
// later rejected admission cannot roll back cleanup and permanently wedge it.
func (r *executionOutboxRepo) expireReservations(ctx context.Context) error {
	for {
		count := 0
		if err := r.d.ExecutionDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var ids []string
			if err := tx.Model(&executionOutboxModel{}).Where("evidence_deadline > 0 AND evidence_deadline <= ? AND (header_reserved > 0 OR terminal_reserved > 0 OR final_reserved > 0)", time.Now().Unix()).Order("evidence_deadline ASC, id ASC").Limit(16).Pluck("id", &ids).Error; err != nil {
				return err
			}
			count = len(ids)
			if count == 0 {
				return nil
			}
			return tx.Model(&executionOutboxModel{}).Where("id IN ?", ids).Updates(map[string]any{"header_reserved": 0, "terminal_reserved": 0, "final_reserved": 0}).Error
		}); err != nil {
			return err
		}
		if count < 16 {
			return nil
		}
	}
}

func outboxIntentDigest(i execution.Intent) (string, error) {
	b, err := jsonx.Marshal(i)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// Backfill pending intent rows written by the earlier local schema. ACKed
// pre-upgrade payloads were already cleared, so their late evidence is accepted
// through the ordinary bounded path; new admissions always reserve capacity.
func restoreOutboxReservations(db *gorm.DB) error {
	for {
		var rows []executionOutboxModel
		// Bound upgrade memory to at most 64 legal intent payloads. Other event
		// categories do not own reservations and need no backfill.
		if err := db.Where("intent_digest = '' AND payload <> '' AND substr(id, -9) = ?", "/intent/0").Order("id ASC").Limit(64).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			var e execution.Event
			if err := jsonx.Unmarshal([]byte(row.Payload), &e); err != nil || e.Category != "intent" {
				return biz.ErrExecutionReceipt
			}
			_, digest, err := e.Encode()
			if err != nil || digest != row.Digest {
				return biz.ErrExecutionReceipt
			}
			id, err := outboxIntentDigest(e.Intent)
			if err != nil {
				return err
			}
			header, terminal, final := int64(0), int64(0), int64(0)
			if e.Intent.EvidenceDeadline > time.Now().Unix() {
				var count int64
				prefix := e.Intent.Key() + "/terminal/"
				if err := db.Model(&executionOutboxModel{}).Where("substr(id, 1, ?) = ?", len(prefix), prefix).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					terminal, header = 1, 1
					// evidence.Do writes response headers as the first checkpoint.
					// Its ACK tombstone is sufficient even after payload compaction.
					if err := db.Model(&executionOutboxModel{}).Where("id = ?", e.Intent.Key()+"/checkpoint/1").Count(&count).Error; err != nil {
						return err
					}
					if count > 0 {
						header = 0
					}
				}
				if e.Intent.Purpose == "user" {
					if err := db.Model(&executionOutboxModel{}).Where("id = ?", e.Intent.Key()+"/final/0").Count(&count).Error; err != nil {
						return err
					}
					if count == 0 {
						final = 1
					}
				}
			}
			if err := db.Model(&executionOutboxModel{}).Where("id = ?", row.ID).Updates(map[string]any{"intent_digest": id, "evidence_deadline": e.Intent.EvidenceDeadline, "header_reserved": header, "terminal_reserved": terminal, "final_reserved": final}).Error; err != nil {
				return err
			}
		}
	}
}
func (r *executionOutboxRepo) Pending(ctx context.Context, limit int) ([]biz.PendingExecution, error) {
	var rows []executionOutboxModel
	if err := r.d.ExecutionDB.WithContext(ctx).Where("status = ? AND next_retry <= ?", "pending", time.Now().Unix()).Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]biz.PendingExecution, 0, len(rows))
	for _, row := range rows {
		var e execution.Event
		if jsonx.Unmarshal([]byte(row.Payload), &e) != nil {
			return nil, biz.ErrExecutionReceipt
		}
		_, d, err := e.Encode()
		if err != nil || d != row.Digest {
			return nil, biz.ErrExecutionReceipt
		}
		out = append(out, biz.PendingExecution{Event: e, Digest: d, Telemetry: requesttrace.Carrier{ObservationID: row.ObservationID, TraceParent: row.TraceParent, TraceState: row.TraceState, AcceptedAtMS: row.AcceptedAtMS, QueuedAtMS: row.QueuedAtMS}})
	}
	return out, nil
}
func (r *executionOutboxRepo) Acknowledge(ctx context.Context, id, d string) error {
	result := r.d.ExecutionDB.WithContext(ctx).Model(&executionOutboxModel{}).Where("id = ? AND digest = ?", id, d).Updates(map[string]any{"status": "acknowledged", "payload": "", "acknowledged_at": gorm.Expr("CASE WHEN acknowledged_at = 0 THEN ? ELSE acknowledged_at END", time.Now().Unix())})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return biz.ErrExecutionReceipt
	}
	return nil
}
func (r *executionOutboxRepo) Defer(ctx context.Context, id string, t time.Time) error {
	return r.d.ExecutionDB.WithContext(ctx).Model(&executionOutboxModel{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]any{"next_retry": t.Unix(), "telemetry_queued_at_ms": time.Now().UnixMilli()}).Error
}
func (r *executionOutboxRepo) Send(ctx context.Context, e execution.Event) (execution.Receipt, error) {
	if r.d.ExecutionSender == nil {
		return execution.Receipt{}, biz.ErrExecutionReceipt
	}
	return r.d.ExecutionSender.Send(ctx, e)
}

func (r *executionOutboxRepo) Stats(ctx context.Context) (int64, int64, error) {
	var row struct {
		Count  int64
		Oldest int64
	}
	err := r.d.ExecutionDB.WithContext(ctx).Model(&executionOutboxModel{}).Where("status = ?", "pending").Select("COUNT(*) AS count, COALESCE(MIN(created_at),0) AS oldest").Scan(&row).Error
	return row.Count, row.Oldest, err
}
