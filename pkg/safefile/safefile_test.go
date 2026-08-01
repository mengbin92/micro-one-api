package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileCleansPathAndReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "..", "secret.txt")
	cleanPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(cleanPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret" {
		t.Fatalf("ReadFile = %q", got)
	}
}

func TestReadFileRejectsDirectory(t *testing.T) {
	if _, err := ReadFile(t.TempDir()); err == nil {
		t.Fatal("ReadFile accepted a directory")
	}
}

func TestCleanFilePathRejectsTraversalOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	// Create a sibling directory the traversal would escape into.
	sibling := filepath.Join(filepath.Dir(dir), "sibling_secret")
	_ = os.WriteFile(sibling, []byte("leaked"), 0o600)
	defer os.Remove(sibling)

	// A path that resolves outside dir must be rejected when dir is the root.
	escape := filepath.Join(dir, "..", "sibling_secret")
	_, err := CleanFilePath(escape, dir)
	if err == nil {
		t.Fatal("CleanFilePath accepted a path escaping allowedRoot")
	}
}

func TestCleanFilePathAllowsPathInsideRoot(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "secret.txt")
	_ = os.WriteFile(inner, []byte("ok"), 0o600)

	got, err := CleanFilePath(inner, dir)
	if err != nil {
		t.Fatalf("CleanFilePath inside root failed: %v", err)
	}
	if got != inner {
		t.Fatalf("CleanFilePath = %q, want %q", got, inner)
	}
}

func TestCleanFilePathRootBoundaryNotPrefix(t *testing.T) {
	dir := t.TempDir()
	// A directory whose name starts with the root prefix must NOT be accepted.
	// e.g. root=/tmp/xxx, attacker path=/tmp/xxx-secret/leak
	decoy := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-secret", "leak")
	_ = os.MkdirAll(filepath.Dir(decoy), 0o755)
	_ = os.WriteFile(decoy, []byte("leaked"), 0o600)
	defer os.RemoveAll(filepath.Dir(decoy))

	_, err := CleanFilePath(decoy, dir)
	if err == nil {
		t.Fatal("CleanFilePath accepted a prefix-spoofed path (boundary check failed)")
	}
}

func TestReadFileWithRootsRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	sibling := filepath.Join(filepath.Dir(dir), "sibling_read")
	_ = os.WriteFile(sibling, []byte("leaked"), 0o600)
	defer os.Remove(sibling)

	_, err := ReadFile(filepath.Join(dir, "..", "sibling_read"), dir)
	if err == nil {
		t.Fatal("ReadFile accepted traversal outside allowedRoot")
	}
}
