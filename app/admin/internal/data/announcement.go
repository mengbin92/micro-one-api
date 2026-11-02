package data

import (
	"context"
	"errors"
	"strconv"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

type announcementRepo struct{ data *Data }

func NewAnnouncementRepo(data *Data) biz.AnnouncementRepo { return &announcementRepo{data: data} }

type announcementModel struct {
	ID                                     string `gorm:"primaryKey"`
	Revision                               int64
	Title, Body, Audience, Targets, Status string
	PublishAt, ExpiresAt, UpdatedAt        int64
}

func (announcementModel) TableName() string { return "announcements" }

type announcementRead struct {
	AnnouncementID string `gorm:"primaryKey"`
	UserID         int64  `gorm:"primaryKey"`
	Revision       int64  `gorm:"primaryKey"`
	ReadAt         int64
}

func (announcementRead) TableName() string { return "announcement_reads" }

type announcementEvent struct {
	ID, AnnouncementID string
	Revision           int64
	Actor, Reason      string
	At                 int64
}

func (announcementEvent) TableName() string { return "announcement_events" }
func toAnnouncement(row announcementModel) (biz.Announcement, error) {
	a := biz.Announcement{ID: row.ID, Revision: row.Revision, Title: row.Title, Body: row.Body, Audience: row.Audience, Status: row.Status, PublishAt: row.PublishAt, ExpiresAt: row.ExpiresAt, UpdatedAt: row.UpdatedAt}
	if err := jsonx.Unmarshal([]byte(row.Targets), &a.Targets); err != nil {
		return a, err
	}
	return a, nil
}
func (r *announcementRepo) db(ctx context.Context) (*gorm.DB, error) {
	if r == nil || r.data == nil || r.data.DB == nil {
		return nil, biz.ErrAnnouncementUnavailable
	}
	return r.data.DB.WithContext(ctx), nil
}
func announcementRepoError(err error) error {
	var ownerError *kerrors.Error
	if err == nil || errors.As(err, &ownerError) || errors.Is(err, authorization.ErrDenied) {
		return err
	}
	return biz.ErrAnnouncementUnavailable.WithCause(err)
}

func (r *announcementRepo) Save(ctx context.Context, a biz.Announcement, actor, reason string) (saved biz.Announcement, err error) {
	defer func() { err = announcementRepoError(err) }()
	db, err := r.db(ctx)
	if err != nil {
		return a, err
	}
	targets, err := jsonx.Marshal(a.Targets)
	if err != nil {
		return a, err
	}
	creating := a.ID == ""
	if creating {
		if a.Revision != 0 {
			return a, biz.ErrAnnouncementConflict
		}
		a.ID = uuid.NewString()
	} else if _, err := uuid.Parse(a.ID); err != nil || a.Revision <= 0 {
		return a, biz.ErrAnnouncementInvalid
	}
	expected := a.Revision
	a.Revision++
	a.UpdatedAt = time.Now().Unix()
	row := announcementModel{ID: a.ID, Revision: a.Revision, Title: a.Title, Body: a.Body, Audience: a.Audience, Targets: string(targets), Status: a.Status, PublishAt: a.PublishAt, ExpiresAt: a.ExpiresAt, UpdatedAt: a.UpdatedAt}
	err = db.Transaction(func(tx *gorm.DB) error {
		if creating {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			result := tx.Model(&announcementModel{}).Where("id = ? AND revision = ?", a.ID, expected).Select("revision", "title", "body", "audience", "targets", "status", "publish_at", "expires_at", "updated_at").Updates(&row)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return biz.ErrAnnouncementConflict
			}
		}
		if err := tx.Where("announcement_id = ?", a.ID).Delete(&announcementTarget{}).Error; err != nil {
			return err
		}
		for _, target := range a.Targets {
			if err := tx.Create(&announcementTarget{AnnouncementID: a.ID, Target: target}).Error; err != nil {
				return err
			}
		}
		if err := authorization.Require(ctx, "system.content.notice.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
			return err
		}
		return tx.Create(&announcementEvent{ID: uuid.NewString(), AnnouncementID: a.ID, Revision: a.Revision, Actor: actor, Reason: reason, At: a.UpdatedAt}).Error
	})
	return a, err
}

// Targets are normalized rows so SQL filters the audience before pagination.
type announcementTarget struct {
	AnnouncementID string `gorm:"primaryKey"`
	Target         string `gorm:"primaryKey"`
}

func (announcementTarget) TableName() string { return "announcement_targets" }
func fmtID(id int64) string                  { return strconv.FormatInt(id, 10) }
func visibleAnnouncements(db *gorm.DB, s biz.AnnouncementSubject, now int64) *gorm.DB {
	return db.Where("status = ? AND publish_at <= ? AND (expires_at = 0 OR expires_at > ?)", "published", now, now).
		Where("audience = ? OR (audience = ? AND EXISTS (SELECT 1 FROM announcement_targets AS t WHERE t.announcement_id = announcements.id AND t.target = ?)) OR (audience = ? AND EXISTS (SELECT 1 FROM announcement_targets AS t WHERE t.announcement_id = announcements.id AND t.target = ?))", "all", "users", fmtID(s.UserID), "groups", fmtID(s.GroupID))
}
func (r *announcementRepo) List(ctx context.Context, s *biz.AnnouncementSubject, now int64, page, size int32) (items []biz.Announcement, count int64, err error) {
	defer func() { err = announcementRepoError(err) }()
	db, err := r.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := db.Model(&announcementModel{})
	if s != nil {
		query = visibleAnnouncements(query, *s, now)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []announcementModel
	if err := query.Order("publish_at DESC, id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]biz.Announcement, 0, len(rows))
	for _, row := range rows {
		a, err := toAnnouncement(row)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	if s != nil && len(rows) > 0 {
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		var reads []announcementRead
		if err := db.Where("user_id = ? AND announcement_id IN ?", s.UserID, ids).Find(&reads).Error; err != nil {
			return nil, 0, err
		}
		for i := range out {
			for _, read := range reads {
				if read.AnnouncementID == out[i].ID && read.Revision == out[i].Revision {
					out[i].Read = true
				}
			}
		}
	}
	return out, total, nil
}
func (r *announcementRepo) MarkRead(ctx context.Context, id string, revision int64, s biz.AnnouncementSubject, now int64) (err error) {
	defer func() { err = announcementRepoError(err) }()
	db, err := r.db(ctx)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var row announcementModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND revision = ?", id, revision).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrAnnouncementNotFound
		}
		if err != nil {
			return err
		}
		a, err := toAnnouncement(row)
		if err != nil {
			return err
		}
		if !a.Visible(s, now) {
			return biz.ErrAnnouncementNotFound
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&announcementRead{AnnouncementID: id, UserID: s.UserID, Revision: revision, ReadAt: now}).Error
	})
}
