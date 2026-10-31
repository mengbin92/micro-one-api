package data

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
	b "micro-one-api/domain/backup/biz"
	"micro-one-api/platform/database/xdb"
)

func scratchRepo(t *testing.T) *repo {
	t.Helper()
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "backup.db")})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	raw, err := os.ReadFile("../../../migrations/sqlite/147_create_managed_backups.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(raw)).Error)
	return NewRepo(&Data{DB: db}).(*repo)
}
func TestPolicyQueueAndInterruptedReceipt(t *testing.T) {
	r := scratchRepo(t)
	ctx := context.Background()
	p, err := r.Policy(ctx)
	require.NoError(t, err)
	require.False(t, p.Enabled)
	p.Enabled = true
	next, err := r.UpdatePolicy(ctx, p, "root", "nightly recovery")
	require.NoError(t, err)
	require.EqualValues(t, 2, next.Revision)
	_, err = r.UpdatePolicy(ctx, p, "root", "stale overwrite")
	require.ErrorIs(t, err, b.ErrConflict)
	j, err := r.Enqueue(ctx, "manual-backup-0001", "root", "recovery drill", time.Now())
	require.NoError(t, err)
	replay, err := r.Enqueue(ctx, j.ID, "root", "recovery drill", time.Now())
	require.NoError(t, err)
	require.Equal(t, j.ID, replay.ID)
	_, err = r.Enqueue(ctx, j.ID, "someone-else", "recovery drill", time.Now())
	require.ErrorIs(t, err, b.ErrConflict)
	_, err = r.Enqueue(ctx, "manual-backup-0002", "root", "recovery drill", time.Now())
	require.ErrorIs(t, err, b.ErrConflict)
	claim, err := r.Claim(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, j.ID, claim.ID)
	require.Equal(t, "manual", claim.Trigger)
	require.NoError(t, r.ExpireRunning(ctx, time.Now()))
	jobs, err := r.List(ctx, 50)
	require.NoError(t, err)
	require.Equal(t, "uncertain", jobs[0].Status)
	require.ErrorIs(t, r.Finish(ctx, *claim), b.ErrConflict)
	scheduled, err := r.Claim(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, "schedule", scheduled.Trigger)
	scheduled.Status = "succeeded"
	require.NoError(t, r.Finish(ctx, *scheduled))
	same, err := r.Claim(ctx, time.Now())
	require.NoError(t, err)
	require.Nil(t, same, "one immutable scheduled job per policy slot")
	events, err := r.Events(ctx, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 5)
}
func TestRetentionPreservesNewestAndUncertainAndRetriesDeletion(t *testing.T) {
	r := scratchRepo(t)
	ctx := context.Background()
	now := time.Now()
	for i := 0; i < 5; i++ {
		j, err := r.Enqueue(ctx, fmt.Sprintf("backup-receipt-%02d", i), "root", "retention drill", now.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		c, err := r.Claim(ctx, now)
		require.NoError(t, err)
		c.Status = "succeeded"
		if i == 4 {
			c.Status = "uncertain"
		}
		c.ObjectKey = j.ID
		require.NoError(t, r.Finish(ctx, *c))
	}
	foreign, err := r.Enqueue(ctx, "backup-other-target", "root", "another storage namespace", now.Add(10*time.Second))
	require.NoError(t, err)
	claimed, err := r.Claim(ctx, now)
	require.NoError(t, err)
	claimed.StorageID = "other-storage"
	claimed.Status = "succeeded"
	require.NoError(t, r.Finish(ctx, *claimed))
	require.Equal(t, foreign.ID, claimed.ID)
	err = r.Prune(ctx, 2, "", func(context.Context, b.Job) error { return fmt.Errorf("storage unavailable") })
	require.Error(t, err)
	var deleting int64
	require.NoError(t, r.d.DB.Model(&jobRow{}).Where("status = 'deleting'").Count(&deleting).Error)
	require.EqualValues(t, 1, deleting)
	removed := []string{}
	require.NoError(t, r.Prune(ctx, 2, "", func(_ context.Context, j b.Job) error { removed = append(removed, j.ID); return nil }))
	require.ElementsMatch(t, []string{"backup-receipt-00", "backup-receipt-01"}, removed)
	jobs, err := r.List(ctx, 50)
	require.NoError(t, err)
	states := map[string]int{}
	for _, j := range jobs {
		states[j.Status]++
	}
	require.Equal(t, map[string]int{"expired": 2, "succeeded": 3, "uncertain": 1}, states)
}
func TestArchiveAuthenticatesAndChecksEveryRecoveryInput(t *testing.T) {
	key, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	dir := t.TempDir()
	files := map[string]string{}
	for name, content := range map[string]string{"mysql.sql": "CREATE DATABASE fixture;", "redis.rdb": "redis fixture", "config/compose.json": "{}", "config/private.env": "CHANNEL_ENCRYPTION_KEY=offline-fixture"} {
		f := filepath.Join(dir, strings.ReplaceAll(name, "/", "-"))
		require.NoError(t, os.WriteFile(f, []byte(content), 0600))
		files[name] = f
	}
	output := filepath.Join(dir, "backup.age")
	m := Manifest{Format: 1, Project: "source", JobID: "archive-job-12345", Schemas: []string{"oneapi"}}
	require.NoError(t, Seal(output, key.Recipient().String(), m, files))
	got, err := Unseal(output, key.String(), filepath.Join(dir, "restored"), 1<<20)
	require.NoError(t, err)
	require.Len(t, got.Files, 4)
	_, err = Unseal(output, key.String(), filepath.Join(dir, "bounded"), 10)
	require.Error(t, err)
	raw, err := os.ReadFile(output)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "offline-fixture")
	raw[len(raw)-1] ^= 1
	bad := filepath.Join(dir, "tampered.age")
	require.NoError(t, os.WriteFile(bad, raw, 0600))
	_, err = Unseal(bad, key.String(), filepath.Join(dir, "tampered"), 1<<20)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(bad, raw[:len(raw)-32], 0600))
	_, err = Unseal(bad, key.String(), filepath.Join(dir, "truncated"), 1<<20)
	require.Error(t, err)
	files["../escape"] = files["mysql.sql"]
	require.Error(t, Seal(filepath.Join(dir, "unsafe.age"), key.Recipient().String(), m, files))
}
func TestS3RejectsInsecureRemoteAndPrefixEscape(t *testing.T) {
	t.Setenv("BACKUP_TEST_ACCESS", "access")
	t.Setenv("BACKUP_TEST_SECRET", "secret")
	cfg := S3Config{Endpoint: "http://example.com", Bucket: "private", Prefix: "backups", AccessKeyEnv: "BACKUP_TEST_ACCESS", SecretKeyEnv: "BACKUP_TEST_SECRET"}
	_, err := NewObjectStore(cfg)
	require.Error(t, err)
	cfg.Endpoint = "http://127.0.0.1:9000"
	s, err := NewObjectStore(cfg)
	require.NoError(t, err)
	require.True(t, s.validKey(s.Key("job-one")))
	require.False(t, s.validKey("backups/../other/file.tar.gz.age"))
	require.False(t, s.validKey("backups/other/file.tar.gz.age"))
}

func TestHostStateFencesConcurrentCleanup(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	r := &Runner{Config: Config{StateDir: dir}}
	release, err := r.LockHostState()
	require.NoError(t, err)
	_, err = r.LockHostState()
	require.Error(t, err)
	release()
	second, err := r.LockHostState()
	require.NoError(t, err)
	defer second()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "job-interrupted"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "job-interrupted", "private.sql"), []byte("secret"), 0600))
	require.NoError(t, r.CleanInterruptedScratch())
	_, err = os.Stat(filepath.Join(dir, "job-interrupted"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
