package xconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v3/config"
)

func TestSubscribeFile_DeliversUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("v: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updates := make(chan *config.KeyValue, 4)
	stop, err := SubscribeFile(path, func(kv *config.KeyValue) {
		updates <- kv
	})
	if err != nil {
		t.Fatal(err)
	}
	if stop == nil {
		t.Fatal("SubscribeFile returned nil stop on a platform that supports fsnotify")
	}
	defer stop()

	// Allow watcher to register.
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(path, []byte("v: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case kv := <-updates:
		if string(kv.Value) != "v: 2\n" {
			t.Errorf("got %q, want %q", string(kv.Value), "v: 2\n")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no update delivered within 3s")
	}

	// Stop should terminate the goroutine and release the fsnotify watcher.
	stop()
	// Subsequent writes should not deliver anything (and not panic).
	if err := os.WriteFile(path, []byte("v: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-updates:
		// A lingering in-flight delivery is OK; drain.
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSubscribeFile_NilPathOrCallbackIsNoop(t *testing.T) {
	stop, err := SubscribeFile("", func(*config.KeyValue) {})
	if err != nil {
		t.Fatalf("err should be nil on empty path: %v", err)
	}
	if stop != nil {
		t.Fatal("stop should be nil when path is empty")
	}
	stop, err = SubscribeFile("some/path", nil)
	if err != nil {
		t.Fatalf("err should be nil on nil callback: %v", err)
	}
	if stop != nil {
		t.Fatal("stop should be nil when callback is nil")
	}
}
