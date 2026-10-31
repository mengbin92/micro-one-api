package data

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
)

// LockHostState fences orchestration/cleanup after a SQL session is lost. A
// second local process cannot race the old worker's deferred service recovery.
// All instances for one Compose project must use the same private state_dir.
func (r *Runner) LockHostState() (func(), error) {
	if err := privateDir(r.Config.StateDir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(r.Config.StateDir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another process owns backup host state")
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
func (r *Runner) CleanInterruptedScratch() error {
	entries, err := os.ReadDir(r.Config.StateDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "job-") {
			if err = os.RemoveAll(filepath.Join(r.Config.StateDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
