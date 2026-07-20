package xconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v3/config"
)

func TestEnvFileSourceWatcherStopCancelsNext(t *testing.T) {
	source := NewEnvFileSource("config.yaml").(*EnvFileSource)
	defer source.cancel()
	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := watcher.Next(); err == nil {
		t.Fatal("Next returned nil error after Stop")
	}
}

// TestEnvFileSourceWatch_FiresOnModify writes a config file, starts a
// watcher, then rewrites the file. The watcher must deliver a KeyValue
// whose Value matches the new contents (after env expansion).
func TestEnvFileSourceWatch_FiresOnModify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("foo: bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := NewEnvFileSource(path).(*EnvFileSource)
	defer source.cancel()

	// Force the fsnotify watcher to be initialised.
	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}

	// Allow the watch to register before we edit.
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(path, []byte("foo: baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan []*config.KeyValue, 1)
	go func() {
		kvs, _ := watcher.Next()
		done <- kvs
	}()

	select {
	case kvs := <-done:
		if len(kvs) != 1 {
			t.Fatalf("expected 1 KV, got %d", len(kvs))
		}
		want := "foo: baz\n"
		if string(kvs[0].Value) != want {
			t.Errorf("got %q, want %q", string(kvs[0].Value), want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not fire on file modify within 3s")
	}
}

// TestEnvFileSourceWatch_EnvExpansionOnReload confirms that a hot-reloaded
// config block still honours the ${VAR:-default} substitution.
func TestEnvFileSourceWatch_EnvExpansionOnReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := NewEnvFileSource(path).(*EnvFileSource)
	defer source.cancel()

	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	t.Setenv("MOA_TEST_NAME", "after-env")
	if err := os.WriteFile(path, []byte("name: ${MOA_TEST_NAME:-fallback}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = watcher.Stop()
	}()

	kvs, err := watcher.Next()
	if err != nil {
		t.Fatalf("watcher.Next: %v", err)
	}
	want := "name: after-env\n"
	if string(kvs[0].Value) != want {
		t.Errorf("env expansion on reload failed: got %q, want %q", string(kvs[0].Value), want)
	}
}

// TestEnvFileSourceLoad_ReadsCurrentFile is the legacy Load() contract: it
// must always return the current file contents, env-expanded.
func TestEnvFileSourceLoad_ReadsCurrentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("MOA_TEST_L", "resolved")
	if err := os.WriteFile(path, []byte("v: ${MOA_TEST_L:-default}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := NewEnvFileSource(path)
	kvs, err := source.Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(kvs[0].Value) != "v: resolved\n" {
		t.Errorf("Load()=%q, want %q", string(kvs[0].Value), "v: resolved\n")
	}
}
