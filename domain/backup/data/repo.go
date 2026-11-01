package data

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/domain/authorization"
	b "micro-one-api/domain/backup/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
)

// Data holds the shared control-plane SQL pool. It never opens a business
// owner's client from a request. Schema creation is exclusively by migration.
type Data struct{ DB *gorm.DB }
type repo struct{ d *Data }

func NewRepo(d *Data) b.WorkerRepo { return &repo{d: d} }

type policyRow struct {
	ID       int
	Revision int64
	Document string
}

func (policyRow) TableName() string { return "backup_policies" }

type jobRow struct {
	ID, Status, Actor, Reason        string
	Trigger                          string `gorm:"column:trigger_kind"`
	CreatedAt, StartedAt, FinishedAt int64
	StorageID, ObjectKey, SHA256     string
	Bytes                            int64
	ErrorCode, Policy                string
}

func (jobRow) TableName() string { return "backup_jobs" }

type eventRow struct {
	ID, JobID, Action, Actor, Reason string
	At                               int64
}

func (eventRow) TableName() string { return "backup_events" }
func toBiz(row jobRow) (b.Job, error) {
	var p b.Policy
	err := jsonx.Unmarshal([]byte(row.Policy), &p)
	return b.Job{ID: row.ID, Status: row.Status, Trigger: row.Trigger, Actor: row.Actor, Reason: row.Reason, CreatedAt: row.CreatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, StorageID: row.StorageID, ObjectKey: row.ObjectKey, SHA256: row.SHA256, Bytes: row.Bytes, ErrorCode: row.ErrorCode, Policy: p}, err
}
func loadPolicy(db *gorm.DB) (b.Policy, error) {
	var row policyRow
	if err := db.First(&row, 1).Error; err != nil {
		return b.Policy{}, err
	}
	var p b.Policy
	err := jsonx.Unmarshal([]byte(row.Document), &p)
	p.Revision = row.Revision
	return p, err
}
func (r *repo) Policy(ctx context.Context) (b.Policy, error) {
	p, err := loadPolicy(r.d.DB.WithContext(ctx))
	return p, controlError(err)
}
func event(tx *gorm.DB, job, action, actor, reason string) error {
	return tx.Create(&eventRow{ID: uuid.NewString(), JobID: job, Action: action, Actor: actor, Reason: reason, At: time.Now().Unix()}).Error
}
func actor(ctx context.Context, fallback string) string {
	if q, ok := authorization.QueryScopeFromContext(ctx, "system.option.security.update"); ok {
		return strconv.FormatInt(q.ActorID, 10)
	}
	return fallback
}
func (r *repo) UpdatePolicy(ctx context.Context, p b.Policy, who, reason string) (b.Policy, error) {
	who = actor(ctx, who)
	old := p.Revision
	p.Revision++
	raw, err := jsonx.Marshal(p)
	if err != nil {
		return b.Policy{}, err
	}
	err = r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&policyRow{}).Where("id = 1 AND revision = ?", old).Updates(map[string]any{"revision": p.Revision, "document": string(raw)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return b.ErrConflict
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "system.option.security.update", 0); err != nil {
			return err
		}
		return event(tx, "", "policy.update", who, reason)
	})
	return p, controlError(authzquery.RecordWriteFailure(ctx, r.d.DB, "system.option.security.update", 0, err))
}
func (r *repo) Enqueue(ctx context.Context, id, who, reason string, now time.Time) (b.Job, error) {
	var out b.Job
	who = actor(ctx, who)
	err := r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize manual queue admission with policy updates and scheduler claims.
		var p policyRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, 1).Error; err != nil {
			return err
		}
		var row jobRow
		err := tx.Where("id = ?", id).Take(&row).Error
		if err == nil {
			if row.Actor != who || row.Reason != reason || row.Trigger != "manual" {
				return b.ErrConflict
			}
			out, err = toBiz(row)
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.Model(&jobRow{}).Where("status IN ?", []string{"queued", "running"}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return b.ErrConflict
		}
		row = jobRow{ID: id, Status: "queued", Trigger: "manual", Actor: who, Reason: reason, CreatedAt: now.Unix(), Policy: p.Document}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := event(tx, id, "queued", who, reason); err != nil {
			return err
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "system.option.security.update", 0); err != nil {
			return err
		}
		out, err = toBiz(row)
		return err
	})
	return out, controlError(authzquery.RecordWriteFailure(ctx, r.d.DB, "system.option.security.update", 0, err))
}
func (r *repo) List(ctx context.Context, n int) ([]b.Job, error) {
	var rows []jobRow
	err := r.d.DB.WithContext(ctx).Order("created_at DESC, id DESC").Limit(n).Find(&rows).Error
	out := []b.Job{}
	for _, row := range rows {
		j, e := toBiz(row)
		if e != nil {
			return nil, controlError(e)
		}
		out = append(out, j)
	}
	return out, controlError(err)
}
func (r *repo) Events(ctx context.Context, n int) ([]b.Event, error) {
	var rows []eventRow
	err := r.d.DB.WithContext(ctx).Order("at DESC, id DESC").Limit(n).Find(&rows).Error
	out := []b.Event{}
	for _, v := range rows {
		out = append(out, b.Event{ID: v.ID, JobID: v.JobID, Action: v.Action, Actor: v.Actor, Reason: v.Reason, At: v.At})
	}
	return out, controlError(err)
}
func (r *repo) Lead(ctx context.Context, run func(context.Context) error) (bool, error) {
	if r.d.DB.Dialector.Name() != "mysql" {
		return false, fmt.Errorf("backup worker supports MySQL only")
	}
	pool, err := r.d.DB.DB()
	if err != nil {
		return false, err
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var acquired int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK('micro-one-api:backup:v1', 0)").Scan(&acquired); err != nil || acquired != 1 {
		return false, err
	}
	// Never return a connection with a session lock to the pool.
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var v sql.NullInt64
		if e := conn.QueryRowContext(clean, "SELECT RELEASE_LOCK('micro-one-api:backup:v1')").Scan(&v); e != nil || !v.Valid || v.Int64 != 1 {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-work.Done():
				return
			case <-ticker.C:
				var held int
				probe, stop := context.WithTimeout(work, 3*time.Second)
				e := conn.QueryRowContext(probe, "SELECT IS_USED_LOCK('micro-one-api:backup:v1') = CONNECTION_ID()").Scan(&held)
				stop()
				if e != nil || held != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return true, run(work)
}
func (r *repo) Claim(ctx context.Context, now time.Time) (*b.Job, error) {
	var out *b.Job
	err := r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pr policyRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&pr, 1).Error; err != nil {
			return err
		}
		var row jobRow
		err := tx.Where("status = ?", "queued").Order("created_at, id").Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			p, e := loadPolicy(tx)
			if e != nil {
				return e
			}
			if !p.Enabled {
				return nil
			}
			if e = p.Validate(); e != nil {
				return e
			}
			id := fmt.Sprintf("schedule-%d-%d", p.Revision, now.Unix()/(int64(p.IntervalHours)*3600))
			raw, _ := jsonx.Marshal(p)
			row = jobRow{ID: id, Status: "queued", Trigger: "schedule", Actor: "system/backup-scheduler", Reason: "scheduled backup", CreatedAt: now.Unix(), Policy: string(raw)}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil
			}
		} else if err != nil {
			return err
		}
		result := tx.Model(&jobRow{}).Where("id = ? AND status = ?", row.ID, "queued").Updates(map[string]any{"status": "running", "started_at": now.Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return b.ErrConflict
		}
		row.Status = "running"
		row.StartedAt = now.Unix()
		j, e := toBiz(row)
		if e != nil {
			return e
		}
		out = &j
		return event(tx, row.ID, "running", "system/backup-worker", row.Reason)
	})
	return out, controlError(err)
}
func (r *repo) Finish(ctx context.Context, j b.Job) error {
	return r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&jobRow{}).Where("id = ? AND status = 'running'", j.ID).Updates(map[string]any{"status": j.Status, "finished_at": time.Now().Unix(), "storage_id": j.StorageID, "object_key": j.ObjectKey, "sha256": j.SHA256, "bytes": j.Bytes, "error_code": j.ErrorCode})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return b.ErrConflict
		}
		return event(tx, j.ID, j.Status, "system/backup-worker", j.ErrorCode)
	})
}
func (r *repo) ExpireRunning(ctx context.Context, now time.Time) error {
	return r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []jobRow
		if err := tx.Where("status = 'running'").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := tx.Model(&jobRow{}).Where("id = ? AND status = 'running'", row.ID).Updates(map[string]any{"status": "uncertain", "finished_at": now.Unix(), "error_code": "worker_interrupted"}).Error; err != nil {
				return err
			}
			if err := event(tx, row.ID, "uncertain", "system/backup-worker", "worker_interrupted"); err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *repo) Prune(ctx context.Context, keep int, storageID string, remove func(context.Context, b.Job) error) error {
	if keep < 2 {
		return b.ErrInvalid
	}
	var rows, pending []jobRow
	if err := r.d.DB.WithContext(ctx).Where("status = 'succeeded' AND storage_id = ?", storageID).Order("created_at DESC, id DESC").Offset(keep).Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	if err := r.d.DB.WithContext(ctx).Where("status = 'deleting' AND storage_id = ?", storageID).Limit(100).Find(&pending).Error; err != nil {
		return err
	}
	rows = append(pending, rows...)
	for _, row := range rows {
		j, err := toBiz(row)
		if err != nil {
			return err
		}
		if row.Status != "deleting" {
			err = r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				res := tx.Model(&jobRow{}).Where("id = ? AND status = 'succeeded'", j.ID).Update("status", "deleting")
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return b.ErrConflict
				}
				return event(tx, j.ID, "deleting", "system/backup-worker", "retention")
			})
			if err != nil {
				return err
			}
		}
		if err = remove(ctx, j); err != nil {
			return err
		}
		if err = r.d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&jobRow{}).Where("id = ? AND status = 'deleting'", j.ID).Update("status", "expired").Error; err != nil {
				return err
			}
			return event(tx, j.ID, "expired", "system/backup-worker", "retention")
		}); err != nil {
			return err
		}
	}
	return nil
}

type workerRow struct {
	ID               int
	LastSeen         int64
	Phase, ErrorCode string
	TimeoutMinutes   int32
}

func (workerRow) TableName() string { return "backup_worker_state" }
func (r *repo) WorkerState(ctx context.Context) (b.WorkerState, error) {
	var v workerRow
	err := r.d.DB.WithContext(ctx).First(&v, 1).Error
	return b.WorkerState{LastSeen: v.LastSeen, Phase: v.Phase, ErrorCode: v.ErrorCode, TimeoutMinutes: v.TimeoutMinutes}, controlError(err)
}
func (r *repo) SetWorkerState(ctx context.Context, v b.WorkerState) error {
	return r.d.DB.WithContext(ctx).Model(&workerRow{}).Where("id = 1").Updates(map[string]any{"last_seen": v.LastSeen, "phase": v.Phase, "error_code": v.ErrorCode, "timeout_minutes": v.TimeoutMinutes}).Error
}

func controlError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{b.ErrInvalid, b.ErrConflict, b.ErrUnavailable, authorization.ErrDenied, authorization.ErrWriteStorageUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return b.ErrUnavailable
}
