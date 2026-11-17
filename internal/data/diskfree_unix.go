//go:build unix

package data

import (
	"path/filepath"
	"syscall"
)

// filesystemFreeBytes reports the free bytes available to this process on the
// filesystem holding path. It is a pre-check only, never a reservation: other
// processes on the same disk can consume the headroom afterwards.
func filesystemFreeBytes(path string) (int64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &stat); err != nil {
		return 0, false
	}
	// #nosec G115 -- block size and available blocks are non-negative kernel values.
	return int64(stat.Bavail) * int64(stat.Bsize), true
}
