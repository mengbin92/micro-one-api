//go:build !unix

package data

// filesystemFreeBytes is unavailable on this platform; the caller falls back to
// the queue's own byte budget instead of blocking writes.
func filesystemFreeBytes(string) (int64, bool) { return 0, false }
