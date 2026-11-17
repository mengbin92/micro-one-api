package data

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
)

// observationOutboxModel is the relay-local durable queue for observation-only
// usage evidence. It is deliberately a separate table (and normally a separate
// database file) from the financial execution outbox.
type observationOutboxModel struct {
	ObservationID string `gorm:"column:observation_id;index"`
	SendID        string `gorm:"column:send_id;index"`
	TraceParent   string `gorm:"column:trace_parent"`
	TraceState    string `gorm:"column:trace_state"`
	AcceptedAtMS  int64  `gorm:"column:telemetry_accepted_at_ms"`
	QueuedAtMS    int64  `gorm:"column:telemetry_queued_at_ms"`

	ID             string `gorm:"primaryKey"`
	Digest         string
	Payload        string
	Category       string `gorm:"index"`
	Status         string `gorm:"index"`
	NextRetry      int64  `gorm:"index"`
	CreatedAt      int64  `gorm:"index"`
	AcknowledgedAt int64  `gorm:"not null;default:0"`
	SignalAt       int64  `gorm:"not null;default:0;index"`
	SignalProtocol string `gorm:"not null;default:''"`
	SignalSource   string `gorm:"not null;default:''"`
	SignalStatus   string `gorm:"not null;default:''"`
	SignalGap      string `gorm:"not null;default:''"`
}

func (observationOutboxModel) TableName() string { return "observation_outbox" }

const (
	observationOutboxMaxEvents = 5000
	// observationOutboxMaxPendingBytes bounds live pending payload, not the
	// physical file: ACK frees payload pages for reuse, so a drained queue must
	// accept new events again without an operator deleting the database.
	observationOutboxMaxPendingBytes = 64 << 20
	observationOutboxDBBytes         = 96 << 20
	observationRetention             = 7 * 24 * time.Hour
	observationMaintenanceBatch      = 128
	observationMaintenanceInterval   = 10
	// observationDefaultMinFreeBytes reserves headroom for everything else on
	// the shared filesystem: the financial outbox needs room to finish its
	// commits and WAL checkpoints, and the services still need to write. The
	// observation queue only consumes what is left above this floor.
	observationDefaultMinFreeBytes = 256 << 20
)

type observationOutboxRepo struct {
	d         *Data
	path      string
	minFree   int64
	maxEvents int64
	maxBytes  int64
	pageSize  int64
	lastMaint atomic.Int64
}

// NewObservationData opens the observation-only outbox database. Permissions,
// WAL, synchronous and capacity limits follow the proven financial outbox
// pattern while keeping an independent budget.
func NewObservationData(path string, sender biz.UsageObservationSender) (*Data, func(), error) {
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("observation outbox path must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	// #nosec G302 -- private directory; owner execute permission is required to reach its 0600 database.
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, nil, err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, nil, fmt.Errorf("observation outbox must be a regular file")
	}
	// #nosec G304 -- absolute path is trusted server configuration, not request input; symlinks rejected above.
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
		return nil, nil, fmt.Errorf("observation outbox page size unavailable")
	}
	for _, query := range []string{fmt.Sprintf("PRAGMA max_page_count=%d", observationOutboxDBBytes/pageSize), "PRAGMA journal_size_limit=1048576", fmt.Sprintf("PRAGMA wal_autocheckpoint=%d", max(int64(1), (1<<20)/pageSize))} {
		if err = db.Exec(query).Error; err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	if err = db.AutoMigrate(&observationOutboxModel{}); err != nil {
		cleanup()
		return nil, nil, err
	}
	// Old schemas did not record receipt time; start their retention clock at
	// upgrade instead of deleting delayed deliveries by creation time.
	if err = db.Model(&observationOutboxModel{}).Where("status = ? AND acknowledged_at = 0", "acknowledged").Update("acknowledged_at", time.Now().Unix()).Error; err != nil {
		cleanup()
		return nil, nil, err
	}
	return &Data{ObservationDB: db, ObservationSender: sender}, cleanup, nil
}

func NewObservationOutboxRepo(d *Data, path string) (biz.UsageObservationRepo, error) {
	repo := &observationOutboxRepo{d: d, path: path, minFree: observationDefaultMinFreeBytes,
		maxEvents: observationOutboxMaxEvents, maxBytes: observationOutboxMaxPendingBytes}
	for _, limit := range []struct {
		key     string
		value   *int64
		minimum int64
	}{
		{"RELAY_OBSERVATION_MIN_FREE_BYTES", &repo.minFree, 0},
		{"RELAY_OBSERVATION_MAX_PENDING_EVENTS", &repo.maxEvents, 1},
		{"RELAY_OBSERVATION_MAX_PENDING_BYTES", &repo.maxBytes, 1},
	} {
		if raw := os.Getenv(limit.key); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < limit.minimum {
				return nil, fmt.Errorf("%s must be an integer >= %d", limit.key, limit.minimum)
			}
			*limit.value = parsed
		}
	}
	var actual int64
	if err := d.ObservationDB.Raw("PRAGMA page_size").Scan(&actual).Error; err != nil {
		return nil, fmt.Errorf("observation page size: %w", err)
	}
	if actual <= 0 {
		return nil, fmt.Errorf("observation page size unavailable")
	}
	repo.pageSize = actual
	return repo, nil
}

// Put is the synchronous local persistence step. Capacity is judged from live
// pending payload and filesystem headroom, never from the allocated file size:
// acknowledged rows keep their pages for reuse, so a drained queue must accept
// new events again without manual maintenance.
func (r *observationOutboxRepo) Put(ctx context.Context, e evidence.SendEvent) error {
	b, digest, err := e.Encode()
	if err != nil {
		return err
	}
	// Independent, bounded maintenance commits before the capacity decision.
	r.maintain(ctx)
	err = r.d.ObservationDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old observationOutboxModel
		if err := tx.Where("id = ?", e.ID).Limit(1).Find(&old).Error; err != nil {
			return err
		}
		if old.ID != "" {
			if old.Digest != digest {
				return biz.ErrObservationReceipt
			}
			return nil
		}
		var capacity struct {
			Pending      int64
			PendingBytes int64
		}
		if err := tx.Model(&observationOutboxModel{}).Where("status = ?", "pending").
			Select("COUNT(*) AS pending, COALESCE(SUM(LENGTH(CAST(payload AS BLOB))),0) AS pending_bytes").Scan(&capacity).Error; err != nil {
			return err
		}
		if capacity.Pending+1 > r.maxEvents || capacity.PendingBytes+int64(len(b)) > r.maxBytes {
			return biz.ErrObservationCapacity
		}
		if r.minFree > 0 {
			if free, ok := filesystemFreeBytes(r.path); ok && free < r.minFree {
				return biz.ErrObservationCapacity
			}
		}
		now := time.Now().Unix()
		telemetry := requesttrace.Capture(ctx)
		row := observationOutboxModel{ObservationID: e.Send.ObservationID, SendID: e.Send.SendID, TraceParent: telemetry.TraceParent, TraceState: telemetry.TraceState, AcceptedAtMS: telemetry.AcceptedAtMS, QueuedAtMS: telemetry.QueuedAtMS, ID: e.ID, Digest: digest, Payload: string(b), Category: e.Category, Status: "pending", CreatedAt: now}
		signal := biz.ObservationSignalFor(e)
		row.SignalAt, row.SignalProtocol, row.SignalSource, row.SignalStatus, row.SignalGap = signal.At, signal.Protocol, signal.SourceKind, signal.Status, signal.Gap
		return tx.Create(&row).Error
	})
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrFull {
		return fmt.Errorf("%w: %w", biz.ErrObservationCapacity, err)
	}
	return err
}

// maintain runs bounded, independently committed cleanup so retention and WAL
// growth cannot be rolled back by a rejected admission. It is throttled so a
// burst of small events does not pay for four deletes and a checkpoint each.
func (r *observationOutboxRepo) maintain(ctx context.Context) {
	now := time.Now().Unix()
	last := r.lastMaint.Load()
	if now-last < observationMaintenanceInterval {
		return
	}
	if !r.lastMaint.CompareAndSwap(last, now) {
		return
	}
	for batch := 0; batch < 4; batch++ {
		cutoff := time.Now().Add(-observationRetention).Unix()
		result := r.d.ObservationDB.WithContext(ctx).
			Where("id IN (?)", r.d.ObservationDB.Model(&observationOutboxModel{}).
				Select("id").Where("(status = ? AND acknowledged_at > 0 AND acknowledged_at < ?) OR (status = ? AND signal_at > 0 AND signal_at < ?)", "acknowledged", cutoff, "quarantined", cutoff).
				Order("acknowledged_at ASC").Limit(observationMaintenanceBatch)).
			Delete(&observationOutboxModel{})
		if result.Error != nil || result.RowsAffected < observationMaintenanceBatch {
			break
		}
	}
	// Best effort: a busy checkpoint must not fail the caller.
	_ = r.d.ObservationDB.WithContext(ctx).Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error
}

func (r *observationOutboxRepo) Pending(ctx context.Context, limit int) ([]biz.PendingObservation, error) {
	var rows []observationOutboxModel
	if err := r.d.ObservationDB.WithContext(ctx).Where("status = ? AND next_retry <= ?", "pending", time.Now().Unix()).Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]biz.PendingObservation, 0, len(rows))
	for _, row := range rows {
		var e evidence.SendEvent
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(row.Payload)))
		valid := digest == row.Digest && jsonx.Unmarshal([]byte(row.Payload), &e) == nil
		if valid {
			_, canonicalDigest, err := e.Encode()
			valid = err == nil && canonicalDigest == row.Digest && e.ID == row.ID && e.Send.SendID == row.SendID && e.Send.ObservationID == row.ObservationID && e.Category == row.Category
		}
		if !valid {
			stamp := time.Now().Unix()
			result := r.d.ObservationDB.WithContext(ctx).Model(&observationOutboxModel{}).Where("id = ? AND status = ? AND digest = ?", row.ID, "pending", row.Digest).
				Updates(map[string]any{"status": "quarantined", "signal_at": stamp, "signal_gap": "rejected", "signal_protocol": "", "signal_source": "", "signal_status": ""})
			if result.Error != nil {
				return nil, result.Error
			}
			if result.RowsAffected > 0 {
				metrics.RecordUsageObservationGap("rejected", float64(stamp))
			}
			continue
		}
		out = append(out, biz.PendingObservation{Event: e, Digest: digest, Telemetry: requesttrace.Carrier{ObservationID: row.ObservationID, TraceParent: row.TraceParent, TraceState: row.TraceState, AcceptedAtMS: row.AcceptedAtMS, QueuedAtMS: row.QueuedAtMS}})
	}
	return out, nil
}

func (r *observationOutboxRepo) Acknowledge(ctx context.Context, id, digest string) error {
	result := r.d.ObservationDB.WithContext(ctx).Model(&observationOutboxModel{}).Where("id = ? AND digest = ?", id, digest).
		Updates(map[string]any{"status": "acknowledged", "payload": "", "acknowledged_at": gorm.Expr("CASE WHEN acknowledged_at = 0 THEN ? ELSE acknowledged_at END", time.Now().Unix())})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return biz.ErrObservationReceipt
	}
	return nil
}

func (r *observationOutboxRepo) Defer(ctx context.Context, id string, t time.Time) error {
	return r.d.ObservationDB.WithContext(ctx).Model(&observationOutboxModel{}).Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]any{"next_retry": t.Unix(), "telemetry_queued_at_ms": time.Now().UnixMilli()}).Error
}

func (r *observationOutboxRepo) Stats(ctx context.Context) (biz.ObservationOutboxStats, error) {
	var row struct {
		Count  int64
		Oldest int64
	}
	err := r.d.ObservationDB.WithContext(ctx).Model(&observationOutboxModel{}).Where("status = ?", "pending").
		Select("COUNT(*) AS count, COALESCE(MIN(created_at),0) AS oldest").Scan(&row).Error
	if err != nil {
		return biz.ObservationOutboxStats{}, err
	}
	stats := biz.ObservationOutboxStats{Pending: row.Count, OldestUnix: row.Oldest}
	var pageCount int64
	if err := r.d.ObservationDB.WithContext(ctx).Raw("PRAGMA page_count").Scan(&pageCount).Error; err != nil {
		return biz.ObservationOutboxStats{}, err
	}
	stats.Bytes = pageCount * r.pageSize
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(r.path + suffix); err == nil {
			stats.Bytes += info.Size()
		}
	}
	var signals []struct {
		At                                int64
		Protocol, SourceKind, Status, Gap string
	}
	if err := r.d.ObservationDB.WithContext(ctx).Model(&observationOutboxModel{}).
		Where("signal_at >= ?", time.Now().Add(-15*time.Minute).Unix()).
		Select("MAX(signal_at) AS at, signal_protocol AS protocol, signal_source AS source_kind, signal_status AS status, signal_gap AS gap").
		Group("signal_protocol, signal_source, signal_status, signal_gap").Scan(&signals).Error; err != nil {
		return biz.ObservationOutboxStats{}, err
	}
	for _, signal := range signals {
		stats.Signals = append(stats.Signals, biz.ObservationSignal{At: signal.At, Protocol: signal.Protocol, SourceKind: signal.SourceKind, Status: signal.Status, Gap: signal.Gap})
	}
	return stats, nil
}
