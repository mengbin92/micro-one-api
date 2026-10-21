package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteMemoryReport(t *testing.T) {
	report := []byte(`{"memory.peak":123}`)
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("TIMING_MEMORY_OUTPUT", "")
		require.NoError(t, WriteMemoryReport(report))
	})
	for _, relative := range []bool{false, true} {
		name := "absolute"
		if relative {
			name = "relative"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "memory.json")
			if relative {
				t.Chdir(dir)
				t.Setenv("TIMING_MEMORY_OUTPUT", "memory.json")
			} else {
				t.Setenv("TIMING_MEMORY_OUTPUT", path)
			}
			require.NoError(t, WriteMemoryReport(report))
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, report, contents)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		})
	}
	t.Run("missing parent", func(t *testing.T) {
		t.Setenv("TIMING_MEMORY_OUTPUT", filepath.Join(t.TempDir(), "missing", "memory.json"))
		require.Error(t, WriteMemoryReport(report))
	})
	t.Run("symlink escape", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "reports")
		require.NoError(t, os.Mkdir(dir, 0700))
		outside := filepath.Join(root, "outside.json")
		require.NoError(t, os.WriteFile(outside, []byte("unchanged"), 0600))
		path := filepath.Join(dir, "memory.json")
		require.NoError(t, os.Symlink(outside, path))
		t.Setenv("TIMING_MEMORY_OUTPUT", path)
		require.Error(t, WriteMemoryReport(report))
		contents, err := os.ReadFile(outside)
		require.NoError(t, err)
		require.Equal(t, "unchanged", string(contents))
	})
}
