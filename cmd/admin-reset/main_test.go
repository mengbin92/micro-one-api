package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteGeneratedPasswordFileCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-password.txt")

	if err := writeGeneratedPasswordFile(path, "secret-pass"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "secret-pass" {
		t.Fatalf("password file content = %q", data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("password file mode = %o, want 0600", got)
	}

	if err := writeGeneratedPasswordFile(path, "new-pass"); err == nil {
		t.Fatal("writeGeneratedPasswordFile overwrote an existing file")
	}
}
