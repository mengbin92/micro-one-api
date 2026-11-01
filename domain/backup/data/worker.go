package data

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	b "micro-one-api/domain/backup/biz"
	"micro-one-api/pkg/safecast"
)

// Tick is called only while holding the cross-process MySQL leader lock.
func Tick(ctx context.Context, repo b.WorkerRepo, runner *Runner, store *ObjectStore) (resultErr error) {
	defer func() {
		status := b.WorkerState{LastSeen: time.Now().Unix(), Phase: "ready", TimeoutMinutes: safecast.IntToInt32Saturating(runner.Config.TimeoutMinutes)}
		if resultErr != nil {
			status.Phase = "attention"
			status.ErrorCode = "backup_tick_failed"
		}
		finish, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, repo.SetWorkerState(finish, status))
	}()
	if err := runner.Recover(ctx); err != nil {
		return fmt.Errorf("resume_required")
	}
	if err := runner.CleanInterruptedScratch(); err != nil {
		return err
	}
	if err := repo.ExpireRunning(ctx, time.Now()); err != nil {
		return err
	}
	if err := store.Preflight(ctx); err != nil {
		return fmt.Errorf("storage_preflight_failed")
	}
	policy, err := repo.Policy(ctx)
	if err != nil {
		return err
	}
	if err = repo.Prune(ctx, int(policy.Retain), store.ID(), func(ctx context.Context, j b.Job) error { return store.Delete(ctx, j.ObjectKey) }); err != nil {
		return fmt.Errorf("retention_failed")
	}
	job, err := repo.Claim(ctx, time.Now().UTC())
	if err != nil || job == nil {
		return err
	}
	if err = repo.SetWorkerState(ctx, b.WorkerState{LastSeen: time.Now().Unix(), Phase: "running", TimeoutMinutes: safecast.IntToInt32Saturating(runner.Config.TimeoutMinutes)}); err != nil {
		return err
	}
	j := *job
	j.StorageID = store.ID()
	work, cancel := context.WithTimeout(ctx, time.Duration(runner.Config.TimeoutMinutes)*time.Minute)
	defer cancel()
	if err = privateDir(runner.Config.StateDir); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(runner.Config.StateDir, "job-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	j.Status = "failed"
	j.ErrorCode = "snapshot_failed"
	m, files, runErr := runner.Snapshot(work, j.ID, dir)
	if runErr == nil {
		archive := filepath.Join(dir, "backup.tar.gz.age")
		j.ErrorCode = "encryption_failed"
		runErr = Seal(archive, runner.Config.Recipient, m, files)
		if runErr == nil {
			j.SHA256, j.Bytes, runErr = fileDigest(archive)
			j.ErrorCode = "upload_uncertain"
			j.ObjectKey = store.Key(j.ID)
			if runErr == nil {
				_, runErr = store.Put(work, j.ObjectKey, archive, j.SHA256)
			}
			if runErr == nil {
				runErr = store.PublishReceipt(work, ObjectReceipt{JobID: j.ID, ObjectKey: j.ObjectKey, SHA256: j.SHA256, Bytes: j.Bytes, SnapshotAt: m.SnapshotAt})
			}
			if runErr == nil {
				j.Status = "succeeded"
				j.ErrorCode = ""
			} else {
				j.Status = "uncertain"
			}
		}
	}
	// A failed resume is never a successful snapshot receipt.
	if _, e := os.Stat(runner.journalPath()); e == nil {
		j.Status = "uncertain"
		j.ErrorCode = "services_resume_failed"
	}
	finish, cancelFinish := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelFinish()
	if err = repo.Finish(finish, j); err != nil {
		return err
	}
	if j.Status != "succeeded" {
		return fmt.Errorf("%s", j.ErrorCode)
	}
	return repo.Prune(ctx, int(j.Policy.Retain), store.ID(), func(ctx context.Context, old b.Job) error { return store.Delete(ctx, old.ObjectKey) })
}
