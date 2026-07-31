package safefile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CleanFilePath resolves path to an absolute, cleaned path and verifies it is
// not a directory.
//
// Security (review pkg-M1): optionally enforce that the resolved path stays
// within one of allowedRoots. When allowedRoots is empty the call is unconstrained
// (backward-compatible behavior for trusted callers such as config loading and
// migration runners). Callers that accept user-controlled or config-derived
// paths MUST pass allowedRoots to prevent directory traversal — e.g.
//
//	CleanFilePath(secretPath, filepath.Join(workdir, "configs"))
//
// rejects "../../etc/passwd" because the cleaned absolute path escapes the root.
func CleanFilePath(path string, allowedRoots ...string) (string, error) {
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if err := enforceWithinRoots(cleanPath, allowedRoots); err != nil {
		return "", err
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", cleanPath)
	}
	return cleanPath, nil
}

// ReadFile reads a file after cleaning its path. It mirrors CleanFilePath's
// optional allowedRoots enforcement. // #nosec G304 -- CleanFilePath normalizes
// and (optionally) sandboxes the path before reading.
func ReadFile(path string, allowedRoots ...string) ([]byte, error) {
	cleanPath, err := CleanFilePath(path, allowedRoots...)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(cleanPath) // #nosec G304 -- CleanFilePath normalizes and verifies a file path.
}

// enforceWithinRoots returns an error if roots is non-empty and cleanPath does
// not resolve inside at least one of them. Comparison is lexical on the cleaned
// absolute paths with an OS separator boundary so "/app/configs-secret" is not
// accepted by a "/app/configs" root.
func enforceWithinRoots(cleanPath string, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	for _, root := range roots {
		absRoot, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		if cleanPath == absRoot {
			return nil
		}
		// Ensure the prefix match respects a path separator boundary so a root
		// like "/app/cfg" does not allow "/app/cfg-secret/leak".
		rel, err := filepath.Rel(absRoot, cleanPath)
		if err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside allowed roots", cleanPath)
}
