package xconfig

import (
	"errors"
	"testing"
)

func TestStoppingFallbackWatcherPreservesSource(t *testing.T) {
	s := NewEnvFileSource("unused").(*EnvFileSource)
	defer s.Close()
	s.watcherOnce.Do(func() { s.watcherErr = errors.New("fsnotify unavailable") })
	first, err := s.Watch()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if s.ctx.Err() != nil {
		t.Fatal("stopping a watcher canceled the source")
	}
	if second.(*noopWatcher).ctx.Err() != nil {
		t.Fatal("stopping a watcher canceled its sibling")
	}
}
