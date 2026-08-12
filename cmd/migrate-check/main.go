// Command migrate-check is the static migration governance gate (v0.19 P1.2).
//
// It verifies, without touching any database:
//
//  1. No dialect directory introduces a NEW duplicate numeric prefix
//     (historical duplicates are documented in dialect-manifest.yaml under
//     duplicate_prefix_allowlist and allowed to stay — the runner versions
//     migrations by full basename, so they do not collide).
//  2. migrations/ownership.yaml references only files that exist, and every
//     runnable root migration is claimed by a service, by shared, or is
//     explicitly exempt (ownership_exempt in the manifest).
//  3. Every root migration with a numeric prefix >= auto_mirror_from_prefix
//     is mirrored verbatim into migrations/postgres/ and migrations/sqlite/
//     (same basename), unless listed under not_applicable.
//  4. The manifest itself is sane: allowlist and not_applicable entries
//     point at files that exist.
//
// Usage: go run ./cmd/migrate-check [-dir ./migrations]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const manifestName = "dialect-manifest.yaml"

var (
	prefixRe  = regexp.MustCompile(`^([0-9]+)_`)
	skipNames = map[string]bool{
		// Skipped by the runner (platform/database/migrate/runner.go).
		"phase3_partitioning.sql": true,
		"schema_split.sql":        true,
	}
)

type manifest struct {
	AutoMirrorFromPrefix string `yaml:"auto_mirror_from_prefix"` // "072" (string, not YAML octal)
	DuplicatePrefix      map[string][]struct {
		Prefix string   `yaml:"prefix"`
		Files  []string `yaml:"files"`
	} `yaml:"duplicate_prefix_allowlist"`
	NotApplicable []struct {
		Version string `yaml:"version"`
		Reason  string `yaml:"reason"`
	} `yaml:"not_applicable"`
	OwnershipExempt []struct {
		File   string `yaml:"file"`
		Reason string `yaml:"reason"`
	} `yaml:"ownership_exempt"`
	HistoricalCoverage map[string]map[string]string `yaml:"historical_coverage"`
}

type ownershipManifest struct {
	Shared   []string            `yaml:"shared"`
	Services map[string][]string `yaml:"services"`
}

func main() {
	dir := flag.String("dir", "./migrations", "migrations directory")
	flag.Parse()

	failed := false
	fail := func(format string, args ...any) {
		failed = true
		fmt.Printf("ERROR: "+format+"\n", args...)
	}
	warn := func(format string, args ...any) {
		fmt.Printf("WARN : "+format+"\n", args...)
	}

	mf, err := loadManifest(filepath.Join(*dir, manifestName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(2)
	}

	root := *dir
	// -- 1. duplicate numeric prefixes per dialect directory -----------------
	dialectDirs := []struct{ name, path string }{
		{"mysql", root},
		{"postgres", filepath.Join(root, "postgres")},
		{"sqlite", filepath.Join(root, "sqlite")},
	}
	for _, d := range dialectDirs {
		checkDuplicatePrefixes(d.name, d.path, mf, fail, warn)
	}

	// -- 2. ownership.yaml validation ---------------------------------------
	ownership, err := loadOwnership(filepath.Join(root, "ownership.yaml"))
	if err != nil {
		fail("ownership.yaml: %v", err)
	} else {
		checkOwnership(root, ownership, mf, fail)
	}

	// -- 3. dialect mirror coverage for new migrations ----------------------
	if mf.AutoMirrorFromPrefix != "" {
		checkMirrorCoverage(root, mf, fail)
	}

	// -- 4. manifest sanity -------------------------------------------------
	checkManifestSanity(root, mf, fail)

	// -- 5. dialect dirs do not silently grow arbitrary non-numbered files --
	checkDialectNonNumbered(dialectDirs, fail)

	if failed {
		fmt.Println("\nmigration-check FAILED")
		os.Exit(1)
	}
	fmt.Println("migration-check OK")
}

func loadManifest(path string) (*manifest, error) {
	// #nosec G304 -- path is built from the operator-supplied -dir flag plus a
	// fixed manifest name; this is a local static-check CLI, not a server.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var mf manifest
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &mf, nil
}

func loadOwnership(path string) (*ownershipManifest, error) {
	// #nosec G304 -- same as loadManifest: local CLI, path from -dir flag.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var om ownershipManifest
	if err := yaml.Unmarshal(data, &om); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &om, nil
}

// sqlFiles returns sorted *.sql basenames in dir (non-recursive).
func sqlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func numericPrefix(name string) (string, bool) {
	m := prefixRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// checkDuplicatePrefixes enforces the duplicate-prefix contract for one
// dialect directory. Historical duplicates are allowed only when the exact
// (dialect, prefix, file set) pair is listed in duplicate_prefix_allowlist.
func checkDuplicatePrefixes(dialect, dir string, mf *manifest, fail, warn func(string, ...any)) {
	files, err := sqlFiles(dir)
	if err != nil {
		fail("read dialect dir %s: %v", dir, err)
		return
	}
	byPrefix := map[string][]string{}
	for _, f := range files {
		if p, ok := numericPrefix(f); ok {
			byPrefix[p] = append(byPrefix[p], strings.TrimSuffix(f, ".sql"))
		}
	}
	allow := map[string]map[string]bool{}
	for _, a := range mf.DuplicatePrefix[dialect] {
		if allow[a.Prefix] == nil {
			allow[a.Prefix] = map[string]bool{}
		}
		for _, f := range a.Files {
			allow[a.Prefix][f] = true
		}
	}
	prefixes := make([]string, 0, len(byPrefix))
	for p := range byPrefix {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		group := byPrefix[p]
		if len(group) <= 1 {
			continue
		}
		allowed := len(allow[p]) > 0
		for _, f := range group {
			if !allow[p][f] {
				allowed = false
				break
			}
		}
		msg := fmt.Sprintf("%s: duplicate numeric prefix %s_ (%s)", dialect, p, strings.Join(group, ", "))
		if allowed {
			warn("%s — historical, allowlisted", msg)
		} else {
			fail("%s — NEW duplicate prefix is forbidden; add to dialect-manifest.yaml only for historical files", msg)
		}
	}
}

// checkOwnership validates ownership.yaml against the files on disk and
// requires every runnable root migration to be claimed or explicitly exempt.
//
// NOTE: a migration may legitimately be claimed by MORE than one service —
// consolidated files such as 000_create_core_tables materialise a subset of
// tables per per-service schema, and shared additive columns (031/067 cache
// token usage, 016 usage log fields) are owned by both billing/log/identity.
// The check therefore only verifies existence and full coverage, not
// single-claimant uniqueness.
func checkOwnership(root string, om *ownershipManifest, mf *manifest, fail func(string, ...any)) {
	// (a) every referenced version must exist.
	seen := map[string]bool{}
	for _, v := range om.Shared {
		seen[v] = true
	}
	for _, list := range om.Services {
		for _, v := range list {
			seen[v] = true
		}
	}
	for v := range seen {
		path := filepath.Join(root, v+".sql")
		if _, err := os.Stat(path); err != nil {
			fail("ownership.yaml references %s but %s does not exist", v, path)
		}
	}

	// (b) every runnable root migration must be owned or exempt.
	exempt := map[string]bool{}
	for _, e := range mf.OwnershipExempt {
		exempt[e.File] = true
	}
	files, err := sqlFiles(root)
	if err != nil {
		fail("read root migrations dir: %v", err)
		return
	}
	for _, f := range files {
		base := strings.TrimSuffix(f, ".sql")
		if _, ok := seen[base]; ok {
			continue
		}
		if skipNames[f] || exempt[f] {
			continue
		}
		// phase1_indexes.sql is applied by the runner but has no numeric
		// prefix; it must be in ownership_exempt.
		fail("migration %s is not claimed in ownership.yaml and not in ownership_exempt", f)
	}
}

// checkMirrorCoverage enforces that root migrations >= auto_mirror_from_prefix
// are mirrored verbatim into postgres/ and sqlite/ unless explicitly N/A.
func checkMirrorCoverage(root string, mf *manifest, fail func(string, ...any)) {
	minPrefix, err := strconv.Atoi(mf.AutoMirrorFromPrefix)
	if err != nil {
		fail("dialect-manifest.yaml auto_mirror_from_prefix %q is not a number", mf.AutoMirrorFromPrefix)
		return
	}
	na := map[string]bool{}
	for _, n := range mf.NotApplicable {
		na[n.Version] = true
	}
	for _, n := range mf.NotApplicable {
		if _, err := os.Stat(filepath.Join(root, n.Version+".sql")); err != nil {
			fail("dialect-manifest.yaml not_applicable lists %s but %s does not exist", n.Version, n.Version+".sql")
		}
	}

	pgFiles := fileSet(root, "postgres")
	sqliteFiles := fileSet(root, "sqlite")

	files, err := sqlFiles(root)
	if err != nil {
		fail("read root migrations dir: %v", err)
		return
	}
	for _, f := range files {
		p, ok := numericPrefix(f)
		if !ok {
			continue
		}
		num, _ := strconv.Atoi(p)
		if num < minPrefix {
			continue
		}
		base := strings.TrimSuffix(f, ".sql")
		if na[base] {
			continue
		}
		for _, dialect := range []string{"postgres", "sqlite"} {
			var set map[string]bool
			if dialect == "postgres" {
				set = pgFiles
			} else {
				set = sqliteFiles
			}
			if !set[f] {
				fail("root migration %s (prefix >= %s) is not mirrored in migrations/%s/; add the identical file or list it under not_applicable", f, mf.AutoMirrorFromPrefix, dialect)
			}
		}
	}
}

func fileSet(root, dialect string) map[string]bool {
	out := map[string]bool{}
	files, err := sqlFiles(filepath.Join(root, dialect))
	if err != nil {
		return out
	}
	for _, f := range files {
		out[f] = true
	}
	return out
}

// checkManifestSanity verifies allowlist entries point at real files and the
// allowlist file sets exactly match what is on disk.
func checkManifestSanity(root string, mf *manifest, fail func(string, ...any)) {
	for dialect, entries := range mf.DuplicatePrefix {
		dir := root
		if dialect != "mysql" {
			dir = filepath.Join(root, dialect)
		}
		onDisk := map[string]bool{}
		if files, err := sqlFiles(dir); err == nil {
			for _, f := range files {
				onDisk[strings.TrimSuffix(f, ".sql")] = true
			}
		}
		for _, e := range entries {
			for _, f := range e.Files {
				if !onDisk[f] {
					fail("dialect-manifest.yaml allowlist references %s/%s.sql which does not exist", dialect, f)
				}
				// The allowlist prefix must match the file's own numeric
				// prefix — otherwise the allowlist silently protects the
				// wrong set of files.
				if got, ok := numericPrefix(f + ".sql"); ok && got != e.Prefix {
					fail("dialect-manifest.yaml allowlist lists %s under prefix %s_ but the file is %s_", f, e.Prefix, got)
				}
			}
		}
	}
	// Historical coverage keys must reference real root migrations (docs
	// only, but a typo here should not be silently trusted).
	for _, cov := range mf.HistoricalCoverage {
		for version := range cov {
			if _, err := os.Stat(filepath.Join(root, version+".sql")); err != nil {
				fail("dialect-manifest.yaml historical_coverage references %s which does not exist in migrations/", version)
			}
		}
	}
}

// checkDialectNonNumbered flags non-numbered .sql files in postgres/sqlite
// dirs (they would be executed by the runner with an unversioned basename,
// which is almost never intended there).
func checkDialectNonNumbered(dirs []struct{ name, path string }, fail func(string, ...any)) {
	for _, d := range dirs {
		if d.name == "mysql" {
			continue
		}
		files, err := sqlFiles(d.path)
		if err != nil {
			fail("read dialect dir %s: %v", d.name, err)
			continue
		}
		for _, f := range files {
			if _, ok := numericPrefix(f); !ok {
				fail("%s: %s has no numeric prefix; dialect dirs must contain only numbered migrations", d.name, f)
			}
		}
	}
}
