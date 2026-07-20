package xconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-kratos/kratos/v3/config"
)

// EnvFileSource reads a config file and expands OS environment variables
// using ${VAR} and ${VAR:-default} syntax before returning it to the
// Kratos config loader.
//
// Phase 2.5 — configuration hot reload: the source also watches the file for
// changes and, on each MODIFY/CREATE event, re-reads it, re-expands the
// environment, and pushes the new KeyValue to every active Watcher's Next()
// call. This is the seam Kratos wires into config.Watch(key, observer) so
// services can subscribe to config changes and reload dependent components
// (model registry, channel cache, resilience thresholds, ...) without a
// restart.
//
// fsnotify is used directly (not via kratos/config/file) because the source
// already expands env vars — the kratos file source returns the raw file
// bytes, which would defeat the ${VAR:-default} substitution this layer
// provides.
type EnvFileSource struct {
	path string

	// mu guards against concurrent registration/removal of watchers while
	// the broadcast goroutine is iterating over the live set.
	mu       sync.RWMutex
	watchers map[*fsnotifyWatcher]struct{}

	// fanout is closed by Close to terminate the broadcaster goroutine.
	fanout chan []*config.KeyValue

	// watcherOnce initialises the underlying fsnotify watcher lazily, on
	// the first Watch() call, so services that never subscribe pay nothing.
	watcherOnce sync.Once
	watcherErr  error

	// ctx/cancel gate the background goroutine.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewEnvFileSource constructs an EnvFileSource rooted at path. The fsnotify
// watcher is not created until the first Watch() call — keeping zero-overhead
// for services that opt out of hot reload.
func NewEnvFileSource(path string) config.Source {
	ctx, cancel := context.WithCancel(context.Background())
	return &EnvFileSource{
		path:     path,
		watchers: make(map[*fsnotifyWatcher]struct{}),
		fanout:   make(chan []*config.KeyValue, 16),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Load reads, env-expands, and returns the current contents of the file.
func (s *EnvFileSource) Load() ([]*config.KeyValue, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	expanded := expandEnv(string(data))
	return []*config.KeyValue{{
		Key:    filepath.Base(s.path),
		Format: filepath.Ext(s.path)[1:],
		Value:  []byte(expanded),
	}}, nil
}

// Watch returns a Watcher whose Next() blocks until the file changes. Each
// change delivers the newly loaded, env-expanded KeyValue to every active
// watcher. A failed fsnotify setup falls back to the historical behaviour
// (a watcher that blocks until Stop) so a missing inotify capability does
// not break startup — services simply don't observe hot reload.
func (s *EnvFileSource) Watch() (config.Watcher, error) {
	// Initialise fsnotify lazily and exactly once.
	s.watcherOnce.Do(func() {
		s.watcherErr = s.startWatcher()
	})
	if s.watcherErr != nil {
		// Fall back to a no-op watcher so the process can still boot.
		return &noopWatcher{ctx: s.ctx, cancel: s.cancel}, nil
	}
	w := &fsnotifyWatcher{
		ch:   make(chan []*config.KeyValue, 4),
		stop: make(chan struct{}),
	}
	s.mu.Lock()
	s.watchers[w] = struct{}{}
	s.mu.Unlock()
	return w, nil
}

// startWatcher opens the fsnotify watcher, registers the file path, and
// launches the broadcast goroutine. It also re-adds the path on Rename
// events (mirrors the kratos/config/file watcher semantics, which handle
// atomic-rename style edits).
func (s *EnvFileSource) startWatcher() error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	// Add the parent directory if the path itself cannot be watched
	// (common on platforms where watching the file directly misses
	// rename-based atomic saves). We watch the file path first and fall
	// back to the directory.
	if err := fw.Add(s.path); err != nil {
		dir := filepath.Dir(s.path)
		if addErr := fw.Add(dir); addErr != nil {
			_ = fw.Close()
			return addErr
		}
	}
	go s.broadcastLoop(fw)
	return nil
}

// broadcastLoop is the fsnotify → watcher fan-out loop. It is the single
// reader of the fsnotify events channel, so re-adding paths on Rename is
// race-free. A debounce window collapses rapid editor saves (which often
// emit CREATE+WRITE+MODIFY in quick succession) into a single reload.
func (s *EnvFileSource) broadcastLoop(w *fsnotify.Watcher) {
	defer w.Close()
	// debounce collapses bursts of editor events (which often emit
	// CREATE+WRITE+MODIFY in quick succession) into a single reload. The
	// timer is reset on every qualifying event so only the last event in a
	// burst triggers reloadAndFanout.
	var debounce *time.Timer
	for {
		select {
		case <-s.ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return
		case event, ok := <-w.Events:
			if !ok {
				if debounce != nil {
					debounce.Stop()
				}
				return
			}
			// Only react to writes/creates/renames that touch our path.
			if event.Name != s.path {
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			// Re-add the watch if the inode changed (rename-based save).
			if event.Op&fsnotify.Rename != 0 || event.Op&fsnotify.Remove != 0 {
				// Best-effort re-add; if it fails the watch is lost until
				// a restart, which still matches kratos semantics.
				_ = w.Add(s.path)
			}
			// Debounce: reset the pending timer so only the last event in a
			// burst triggers a reload.
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(100*time.Millisecond, s.reloadAndFanout)
		case err, ok := <-w.Errors:
			if !ok {
				if debounce != nil {
					debounce.Stop()
				}
				return
			}
			_ = err // fsnotify errors are not actionable for the caller.
		}
	}
}

// Close releases the underlying fsnotify watcher and broadcast goroutine.
// It is safe to call multiple times. The kratos config.Source interface
// does not expose Close, so callers that want to stop the background
// goroutine (e.g. tests, short-lived tools) hold the concrete *EnvFileSource
// and call Close directly; long-lived services leave it running for the
// lifetime of the process.
func (s *EnvFileSource) Close() error {
	s.cancel()
	return nil
}

// reloadAndFanout re-reads the file and pushes the new KeyValue to every
// registered watcher. A read error drops the event (the file may be in a
// partial-write state); the next MODIFY will retry.
func (s *EnvFileSource) reloadAndFanout() {
	kvs, err := s.Load()
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for w := range s.watchers {
		select {
		case w.ch <- kvs:
		default:
			// Watcher's buffer is full — drop the oldest pending delivery
			// to keep the latest config visible to the consumer. We do not
			// block the broadcast loop on a slow subscriber.
			select {
			case <-w.ch:
			default:
			}
			select {
			case w.ch <- kvs:
			default:
			}
		}
	}
}

// unregister removes a watcher from the fan-out set. Called by the watcher's
// Stop().
func (s *EnvFileSource) unregister(w *fsnotifyWatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.watchers, w)
}

// fsnotifyWatcher is the config.Watcher implementation returned by
// EnvFileSource.Watch. Next() blocks until either a new KV arrives from the
// broadcast loop or Stop() is called.
type fsnotifyWatcher struct {
	ch   chan []*config.KeyValue
	stop chan struct{}
	once sync.Once
}

func (w *fsnotifyWatcher) Next() ([]*config.KeyValue, error) {
	select {
	case kv := <-w.ch:
		return kv, nil
	case <-w.stop:
		return nil, context.Canceled
	}
}

func (w *fsnotifyWatcher) Stop() error {
	w.once.Do(func() { close(w.stop) })
	return nil
}

// noopWatcher blocks on Next() until the context is cancelled. Used as the
// fallback when fsnotify cannot be initialised (containers without inotify,
// restricted sandboxes, etc.) so the service still boots.
type noopWatcher struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (w *noopWatcher) Next() ([]*config.KeyValue, error) {
	<-w.ctx.Done()
	return nil, w.ctx.Err()
}

func (w *noopWatcher) Stop() error {
	w.cancel()
	return nil
}

// expandEnv replaces ${VAR} and ${VAR:-default} with environment variable values.
// If VAR is unset and a default is provided, the default is used.
// If VAR is unset and no default is provided, an empty string is used.
func expandEnv(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}")
			if end >= 0 {
				expr := s[i+2 : i+2+end]
				varName, defaultVal, hasDefault := strings.Cut(expr, ":-")
				val, ok := os.LookupEnv(strings.TrimSpace(varName))
				if ok {
					result.WriteString(val)
				} else if hasDefault {
					result.WriteString(defaultVal)
				}
				i = i + 2 + end + 1
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}
