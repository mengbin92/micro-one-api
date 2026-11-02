package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"testing"
	"time"
)

func TestAnnouncementPublicationAndAudience(t *testing.T) {
	a := Announcement{Title: "title", Body: "body", Audience: "groups", Targets: []string{"2"}, Status: "published", PublishAt: 10, ExpiresAt: 20}
	require.NoError(t, a.Validate())
	s := AnnouncementSubject{UserID: 7, GroupID: 2}
	require.False(t, a.Visible(s, 9))
	require.True(t, a.Visible(s, 10))
	require.True(t, a.Visible(s, 19))
	require.False(t, a.Visible(s, 20))
	require.False(t, a.Visible(AnnouncementSubject{UserID: 8, GroupID: 3}, 15))
	a.Audience = "users"
	a.Targets = []string{"7"}
	require.True(t, a.Visible(s, 15))
	a.Status = "draft"
	require.False(t, a.Visible(s, 15))
	a.Status = "archived"
	require.False(t, a.Visible(s, 15))
	a.Status = "published"
	a.Targets = []string{"007"}
	require.ErrorIs(t, a.Validate(), ErrAnnouncementInvalid)
	a.Targets = []string{"7", "7"}
	require.ErrorIs(t, a.Validate(), ErrAnnouncementInvalid)
	a.Targets = nil
	require.ErrorIs(t, a.Validate(), ErrAnnouncementInvalid)
	a.Audience = "all"
	require.NoError(t, a.Validate())
	a.ExpiresAt = 10
	require.ErrorIs(t, a.Validate(), ErrAnnouncementInvalid)
}

type announcementFakeRepo struct{ calls int }

func (r *announcementFakeRepo) Save(_ context.Context, a Announcement, _, _ string) (Announcement, error) {
	r.calls++
	return a, nil
}
func (r *announcementFakeRepo) List(context.Context, *AnnouncementSubject, int64, int32, int32) ([]Announcement, int64, error) {
	r.calls++
	return nil, 0, nil
}
func (r *announcementFakeRepo) MarkRead(context.Context, string, int64, AnnouncementSubject, int64) error {
	r.calls++
	return nil
}
func TestAnnouncementsDenyBeforeStorage(t *testing.T) {
	repo := &announcementFakeRepo{}
	policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{}}
	uc := NewAnnouncementUsecase(repo, policy)
	_, _, err := uc.Managed(authztest.Context(), 1, 20)
	require.Error(t, err)
	require.Zero(t, repo.calls)
	policy.Scopes["system.content.notice.update"] = authztest.All()
	_, _, err = uc.Managed(authztest.Context(), 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	_, _, err = uc.Mine(context.Background(), AnnouncementSubject{}, 1, 20)
	require.ErrorIs(t, err, ErrAnnouncementNotFound)
}

func TestAnnouncementImmediatePublicationFreezesActualTime(t *testing.T) {
	repo := &announcementFakeRepo{}
	uc := NewAnnouncementUsecase(repo, nil)
	a := Announcement{Title: "notice", Body: "body", Audience: "all", Status: "published"}
	before := time.Now().Unix()
	saved, err := uc.Save(context.Background(), a, "publish now", "1")
	require.NoError(t, err)
	require.GreaterOrEqual(t, saved.PublishAt, before)
	require.LessOrEqual(t, saved.PublishAt, time.Now().Unix())
	a.ExpiresAt = before - 1
	_, err = uc.Save(context.Background(), a, "invalid expiration", "1")
	require.ErrorIs(t, err, ErrAnnouncementInvalid)
}
