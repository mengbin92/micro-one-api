package biz

import (
	"fmt"
	"strings"
	"sync/atomic"

	"micro-one-api/pkg/safefile"

	"github.com/bytedance/sonic"
	"gopkg.in/yaml.v3"
)

// ModelEntry represents a single model mapping entry.
type ModelEntry struct {
	ActualName   string   `json:"actual_name" yaml:"actual_name"`
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
}

// modelsFile is the YAML/JSON structure of models config.
type modelsFile struct {
	Models map[string]*ModelEntry `json:"models" yaml:"models"`
}

// KnownCapabilities is the set of valid model capabilities.
var KnownCapabilities = map[string]bool{
	"function_call": true,
	"vision":        true,
	"streaming":     true,
	"embedding":     true,
}

// ModelMapper resolves client-facing model names to actual upstream model names.
//
// Phase 2.5 — hot reload: the mapper caches its source path and exposes
// Reload(), which re-reads and re-validates the file. Swap is atomic: a
// pointer indirection keeps the hot path lock-free while a reload builds a
// fresh snapshot. Concurrent Resolve() callers see either the old or the
// new snapshot, never a partially-mutated map.
type ModelMapper struct {
	path string
	snap atomic.Pointer[map[string]*ModelEntry]
}

// NewModelMapper creates a ModelMapper from a config file path.
// Returns a no-op mapper if path is empty.
// Validates that all entries have non-empty actual_name and known capabilities.
func NewModelMapper(path string) (*ModelMapper, error) {
	m := &ModelMapper{path: path}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload re-reads the source file (if one was configured) and atomically swaps
// the snapshot in use by Resolve/HasCapability. A nil/empty path yields an
// empty no-op mapper that never fails. Validation matches NewModelMapper:
// every entry must have a non-empty actual_name and known capabilities.
func (m *ModelMapper) Reload() error {
	if m == nil {
		return nil
	}
	if m.path == "" {
		empty := make(map[string]*ModelEntry)
		m.snap.Store(&empty)
		return nil
	}

	data, err := safefile.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("failed to read models config %s: %w", m.path, err)
	}

	var file modelsFile
	if isYAMLFile(m.path) {
		if err := yaml.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("failed to parse models config %s: %w", m.path, err)
		}
	} else {
		if err := sonic.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("failed to parse models config %s: %w", m.path, err)
		}
	}

	if file.Models == nil {
		file.Models = make(map[string]*ModelEntry)
	}

	// Validate entries
	for name, entry := range file.Models {
		if entry.ActualName == "" {
			return fmt.Errorf("models config: model %q has empty actual_name", name)
		}
		for _, cap := range entry.Capabilities {
			if !KnownCapabilities[cap] {
				return fmt.Errorf("models config: model %q has unknown capability %q (known: function_call, vision, streaming, embedding)", name, cap)
			}
		}
	}

	m.snap.Store(&file.Models)
	return nil
}

func (m *ModelMapper) modelsSnapshot() map[string]*ModelEntry {
	if m == nil {
		return nil
	}
	if p := m.snap.Load(); p != nil {
		return *p
	}
	return nil
}

// Resolve returns the actual upstream model name for the given client model name.
// If no mapping exists, returns the original name unchanged.
func (m *ModelMapper) Resolve(modelName string) string {
	models := m.modelsSnapshot()
	if entry, ok := models[modelName]; ok && entry.ActualName != "" {
		return entry.ActualName
	}
	return modelName
}

// HasCapability checks if a model has the specified capability.
func (m *ModelMapper) HasCapability(modelName, capability string) bool {
	models := m.modelsSnapshot()
	entry, ok := models[modelName]
	if !ok {
		return false
	}
	for _, cap := range entry.Capabilities {
		if cap == capability {
			return true
		}
	}
	return false
}

// GetEntry returns the full model entry for the given name, or nil if not found.
func (m *ModelMapper) GetEntry(modelName string) *ModelEntry {
	return m.modelsSnapshot()[modelName]
}

// NewModelMapperForTest builds a ModelMapper directly from an in-memory map,
// bypassing the file loader. Intended only for tests; production code should
// use NewModelMapper so Reload() keeps the path for hot-reload.
func NewModelMapperForTest(models map[string]*ModelEntry) *ModelMapper {
	if models == nil {
		models = make(map[string]*ModelEntry)
	}
	m := &ModelMapper{}
	m.snap.Store(&models)
	return m
}

// isYAMLFile checks if a file path has a YAML extension.
func isYAMLFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
