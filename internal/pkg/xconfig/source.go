package xconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-kratos/kratos/v2/config"
)

// EnvFileSource reads a config file and expands OS environment variables
// using ${VAR} and ${VAR:-default} syntax before returning it to the
// Kratos config loader.
type EnvFileSource struct {
	path   string
	ctx    context.Context
	cancel context.CancelFunc
}

func NewEnvFileSource(path string) config.Source {
	return &EnvFileSource{path: path}
}

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

func (s *EnvFileSource) Watch() (config.Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	s.cancel = cancel
	return &noopWatcher{ctx: ctx, cancel: cancel}, nil
}

// noopWatcher blocks on Next() until the context is cancelled.
// Config file changes are not watched in Docker deployments.
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
