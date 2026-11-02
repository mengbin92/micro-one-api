package data

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestAnnouncementsOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			pool, err := db.DB()
			require.NoError(t, err)
			if driver == "sqlite" {
				pool.SetMaxOpenConns(1)
			}
			repo := NewAnnouncementRepo(&Data{DB: db})
			ctx := context.Background()
			now := time.Now().Unix()
			a, err := repo.Save(ctx, biz.Announcement{Title: "维护通知", Body: "plain <script>text</script>", Audience: "groups", Targets: []string{"2"}, Status: "published", PublishAt: now - 1, ExpiresAt: now + 600}, "1", "运营发布")
			require.NoError(t, err)
			require.EqualValues(t, 1, a.Revision)
			mine := biz.AnnouncementSubject{UserID: 7, GroupID: 2}
			other := biz.AnnouncementSubject{UserID: 8, GroupID: 3}
			rows, total, err := repo.List(ctx, &mine, now, 1, 20)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.False(t, rows[0].Read)
			_, total, err = repo.List(ctx, &other, now, 1, 20)
			require.NoError(t, err)
			require.Zero(t, total)
			require.ErrorIs(t, repo.MarkRead(ctx, a.ID, a.Revision, other, now), biz.ErrAnnouncementNotFound)
			require.ErrorIs(t, repo.MarkRead(ctx, a.ID, a.Revision, mine, now+600), biz.ErrAnnouncementNotFound)
			var wg sync.WaitGroup
			readErrors := make(chan error, 12)
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); readErrors <- repo.MarkRead(ctx, a.ID, a.Revision, mine, now) }()
			}
			wg.Wait()
			close(readErrors)
			for err := range readErrors {
				require.NoError(t, err)
			}
			var count int64
			require.NoError(t, db.Model(&announcementRead{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
			rows, _, err = repo.List(ctx, &mine, now, 1, 20)
			require.NoError(t, err)
			require.True(t, rows[0].Read)
			var winners atomic.Int32
			writeErrors := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := repo.Save(ctx, a, "1", "并发修改")
					if err == nil {
						winners.Add(1)
					} else {
						writeErrors <- err
					}
				}()
			}
			wg.Wait()
			close(writeErrors)
			require.EqualValues(t, 1, winners.Load())
			for err := range writeErrors {
				require.ErrorIs(t, err, biz.ErrAnnouncementConflict)
			}
			rows, _, err = repo.List(ctx, &mine, now, 1, 20)
			require.NoError(t, err)
			require.False(t, rows[0].Read)
			require.EqualValues(t, 2, rows[0].Revision)
			require.ErrorIs(t, repo.MarkRead(ctx, a.ID, 1, mine, now), biz.ErrAnnouncementNotFound)
			// Audit failure must roll back both content and normalized recipients.
			require.NoError(t, db.Migrator().DropTable(&announcementEvent{}))
			changed := rows[0]
			changed.Title = "must roll back"
			changed.Targets = []string{"3"}
			_, err = repo.Save(ctx, changed, "1", "审计失败回滚")
			require.ErrorIs(t, err, biz.ErrAnnouncementUnavailable)
			rows, total, err = repo.List(ctx, &mine, now, 1, 20)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Equal(t, a.Title, rows[0].Title)
			require.EqualValues(t, 2, rows[0].Revision)
			_, total, err = repo.List(ctx, &other, now, 1, 20)
			require.NoError(t, err)
			require.Zero(t, total)
		})
	}
}

func TestAnnouncementsFilterBeforePaginationDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := NewAnnouncementRepo(&Data{DB: db})
			ctx := context.Background()
			now := time.Now().Unix()
			for _, a := range []biz.Announcement{
				{Title: "due", Body: "b", Audience: "all", Status: "published", PublishAt: now},
				{Title: "future", Body: "b", Audience: "all", Status: "published", PublishAt: now + 1},
				{Title: "expired", Body: "b", Audience: "all", Status: "published", ExpiresAt: now},
				{Title: "draft", Body: "b", Audience: "all", Status: "draft"},
				{Title: "archived", Body: "b", Audience: "all", Status: "archived"},
				{Title: "other", Body: "b", Audience: "users", Targets: []string{"99"}, Status: "published"},
			} {
				_, err := repo.Save(ctx, a, "1", "audience fixture")
				require.NoError(t, err)
			}
			subject := biz.AnnouncementSubject{UserID: 7, GroupID: 2}
			rows, total, err := repo.List(ctx, &subject, now, 1, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.Equal(t, "due", rows[0].Title)
			rows, total, err = repo.List(ctx, &subject, now, 2, 1)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Empty(t, rows)
		})
	}
}
