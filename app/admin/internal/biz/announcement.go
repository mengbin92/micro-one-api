package biz

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v3/errors"
	v "micro-one-api/api/admin/v1"
	"micro-one-api/domain/authorization"
)

var (
	ErrAnnouncementInvalid     = errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_BUSINESS_RULE_VIOLATION.String(), "invalid announcement")
	ErrAnnouncementNotFound    = errors.NotFound(v.AnnouncementErrorReason_ANNOUNCEMENT_NOT_FOUND.String(), "announcement not found")
	ErrAnnouncementConflict    = errors.Conflict(v.AuthorizationErrorReason_AUTHORIZATION_REVISION_CONFLICT.String(), "announcement changed; reload before saving")
	ErrAnnouncementUnavailable = errors.ServiceUnavailable(v.AuthorizationErrorReason_AUTHORIZATION_DEPENDENCY_UNAVAILABLE.String(), "announcements unavailable")
)

type Announcement struct {
	ID                              string
	Revision                        int64
	Title, Body, Audience, Status   string
	Targets                         []string
	PublishAt, ExpiresAt, UpdatedAt int64
	Read                            bool
}
type AnnouncementSubject struct{ UserID, GroupID int64 }

func (a Announcement) Visible(subject AnnouncementSubject, now int64) bool {
	if subject.UserID <= 0 || a.Status != "published" || a.PublishAt > now || a.ExpiresAt > 0 && a.ExpiresAt <= now {
		return false
	}
	if a.Audience == "all" {
		return true
	}
	id := subject.UserID
	if a.Audience == "groups" {
		id = subject.GroupID
	} else if a.Audience != "users" {
		return false
	}
	for _, target := range a.Targets {
		if target == strconv.FormatInt(id, 10) {
			return true
		}
	}
	return false
}
func (a Announcement) Validate() error {
	if strings.TrimSpace(a.Title) == "" || utf8.RuneCountInString(a.Title) > 120 || strings.TrimSpace(a.Body) == "" || len(a.Body) > 32768 || a.PublishAt < 0 || a.ExpiresAt < 0 || a.PublishAt > 253402271999 || a.ExpiresAt > 253402271999 || a.ExpiresAt > 0 && a.ExpiresAt <= a.PublishAt || a.Revision < 0 {
		return ErrAnnouncementInvalid
	}
	if a.Status != "draft" && a.Status != "published" && a.Status != "archived" {
		return ErrAnnouncementInvalid
	}
	if a.Audience == "all" {
		if len(a.Targets) != 0 {
			return ErrAnnouncementInvalid
		}
		return nil
	}
	if a.Audience != "groups" && a.Audience != "users" || len(a.Targets) == 0 || len(a.Targets) > 200 {
		return ErrAnnouncementInvalid
	}
	seen := map[string]bool{}
	for _, target := range a.Targets {
		id, err := strconv.ParseInt(target, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != target || seen[target] {
			return ErrAnnouncementInvalid
		}
		seen[target] = true
	}
	return nil
}

type AnnouncementRepo interface {
	Save(context.Context, Announcement, string, string) (Announcement, error)
	List(context.Context, *AnnouncementSubject, int64, int32, int32) ([]Announcement, int64, error)
	MarkRead(context.Context, string, int64, AnnouncementSubject, int64) error
}
type AnnouncementUsecase struct {
	repo AnnouncementRepo
	auth authorization.Resolver
}

func NewAnnouncementUsecase(repo AnnouncementRepo, auth authorization.Resolver) *AnnouncementUsecase {
	return &AnnouncementUsecase{repo: repo, auth: auth}
}
func (u *AnnouncementUsecase) prepare(ctx context.Context) (context.Context, error) {
	if u == nil || u.repo == nil {
		return ctx, ErrAnnouncementUnavailable
	}
	ctx, err := authorization.Prepare(ctx, u.auth, "admin.content", "system.content.notice.update")
	if err != nil {
		return ctx, err
	}
	return ctx, authorization.Require(ctx, "system.content.notice.update", authorization.ObjectFacts{Context: authorization.Platform()})
}
func (u *AnnouncementUsecase) Save(ctx context.Context, a Announcement, reason, actor string) (Announcement, error) {
	ctx, err := u.prepare(ctx)
	if err != nil {
		return Announcement{}, err
	}
	if a.Status == "published" && a.PublishAt == 0 {
		a.PublishAt = time.Now().Unix()
	}
	if a.Validate() != nil || utf8.RuneCountInString(strings.TrimSpace(reason)) < 3 || utf8.RuneCountInString(reason) > 240 || actor == "" {
		return Announcement{}, ErrAnnouncementInvalid
	}
	return u.repo.Save(ctx, a, actor, reason)
}
func (u *AnnouncementUsecase) Managed(ctx context.Context, page, size int32) ([]Announcement, int64, error) {
	ctx, err := u.prepare(ctx)
	if err != nil {
		return nil, 0, err
	}
	return u.list(ctx, nil, page, size)
}
func (u *AnnouncementUsecase) Mine(ctx context.Context, subject AnnouncementSubject, page, size int32) ([]Announcement, int64, error) {
	if subject.UserID <= 0 {
		return nil, 0, ErrAnnouncementNotFound
	}
	return u.list(ctx, &subject, page, size)
}
func (u *AnnouncementUsecase) list(ctx context.Context, subject *AnnouncementSubject, page, size int32) ([]Announcement, int64, error) {
	if u == nil || u.repo == nil {
		return nil, 0, ErrAnnouncementUnavailable
	}
	if page < 1 {
		page = 1
	}
	if page > 1000000 || size < 0 || size > 100 {
		return nil, 0, ErrAnnouncementInvalid
	}
	if size == 0 {
		size = 20
	}
	return u.repo.List(ctx, subject, time.Now().Unix(), page, size)
}
func (u *AnnouncementUsecase) Read(ctx context.Context, id string, revision int64, subject AnnouncementSubject) error {
	if u == nil || u.repo == nil {
		return ErrAnnouncementUnavailable
	}
	if subject.UserID <= 0 || id == "" || revision <= 0 {
		return ErrAnnouncementInvalid
	}
	return u.repo.MarkRead(ctx, id, revision, subject, time.Now().Unix())
}
