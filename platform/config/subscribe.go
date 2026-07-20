package xconfig

import (
	"github.com/go-kratos/kratos/v3/config"
)

// SubscribeCallback is invoked once per file-change event delivered by the
// underlying EnvFileSource watcher. The KeyValue is the freshly-loaded,
// env-expanded file contents; the callback decides what to do with it
// (e.g. re-parse and swap an in-memory cache).
type SubscribeCallback func(kv *config.KeyValue)

// SubscribeFile opens an EnvFileSource rooted at path and invokes cb on
// every change. It returns a stop function that terminates the watcher; the
// caller should defer it from the service's cleanup path.
//
// SubscribeFile is intentionally tolerant: if the platform cannot provide
// filesystem notifications (containers without inotify, restricted
// sandboxes), it returns a nil stop and a nil error — the caller proceeds
// without hot reload, matching the historical behaviour.
func SubscribeFile(path string, cb SubscribeCallback) (func(), error) {
	if path == "" || cb == nil {
		return nil, nil
	}
	source, ok := NewEnvFileSource(path).(*EnvFileSource)
	if !ok {
		return nil, nil
	}
	watcher, err := source.Watch()
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			kvs, err := watcher.Next()
			if err != nil {
				return
			}
			for _, kv := range kvs {
				cb(kv)
			}
		}
	}()
	stop := func() {
		_ = watcher.Stop()
		_ = source.Close()
		<-done
	}
	return stop, nil
}
