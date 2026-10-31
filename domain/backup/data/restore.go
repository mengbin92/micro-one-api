package data

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Restore never starts application writers, creates a user, reissues a Token,
// or replays a settlement. The caller performs the owner recovery checks in an
// isolated network before granting any traffic to this new environment.
func (r *Runner) Restore(ctx context.Context, artifact, identity, expectedHash string) (Manifest, error) {
	var empty Manifest
	hash, _, err := fileDigest(artifact)
	if err != nil || len(expectedHash) != 64 || hash != expectedHash {
		return empty, fmt.Errorf("encrypted artifact receipt mismatch")
	}
	if err = privateDir(r.Config.StateDir); err != nil {
		return empty, err
	}
	scratch, err := os.MkdirTemp(r.Config.StateDir, "restore-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(scratch)
	m, err := Unseal(artifact, identity, filepath.Join(scratch, "payload"), 100<<30)
	if err != nil {
		return empty, err
	}
	for _, ref := range []string{"JWT_SECRET_KEY", "CHANNEL_ENCRYPTION_KEY", "IDENTITY_MFA_ENCRYPTION_KEY"} {
		if !slices.Contains(m.KeyReferences, ref) {
			return m, fmt.Errorf("recovery key reference manifest incomplete")
		}
	}
	a, w := slices.Clone(m.Schemas), slices.Clone(r.Config.Schemas)
	slices.Sort(a)
	slices.Sort(w)
	if !slices.Equal(a, w) {
		return m, fmt.Errorf("restore schema set mismatch")
	}
	if m.Project == r.Config.Project {
		return m, fmt.Errorf("restore requires an independent Compose project")
	}
	for _, name := range []string{"mysql", "redis"} {
		cid, e := r.output(ctx, "ps", "-q", name)
		if e != nil {
			return m, e
		}
		var out strings.Builder
		if e = r.command(ctx, &out, nil, "inspect", "--format", "{{.Image}}", cid); e != nil {
			return m, e
		}
		if m.Images[name] == "" || strings.TrimSpace(out.String()) != m.Images[name] {
			return m, fmt.Errorf("restore store image differs from snapshot")
		}
	}
	running, err := r.output(ctx, "ps", "--status", "running", "--services")
	if err != nil {
		return m, err
	}
	for _, name := range strings.Fields(running) {
		if name != "mysql" && name != "redis" {
			return m, fmt.Errorf("restore target application writers must be stopped")
		}
	}
	db, err := sql.Open("mysql", os.Getenv(r.Config.SnapshotDSNEnv))
	if err != nil {
		return m, err
	}
	defer db.Close()
	if err = r.verifyMySQL(ctx, db, false); err != nil {
		return m, err
	}
	var count int64
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','information_schema','performance_schema')").Scan(&count); err != nil {
		return m, err
	}
	if count != 0 {
		return m, fmt.Errorf("restore requires empty MySQL target")
	}
	if err = r.verifyRedis(ctx); err != nil {
		return m, err
	}
	// Restored SQL events must not execute against the recovery environment.
	// The target was proven empty before changing this instance setting.
	if _, err = db.ExecContext(ctx, "SET GLOBAL event_scheduler = OFF"); err != nil {
		return m, fmt.Errorf("cannot disable recovery target event scheduler")
	}
	keyspace, err := r.output(ctx, "exec", "-T", "-e", "REDISCLI_AUTH", "redis", "redis-cli", "-e", "--raw", "INFO", "keyspace")
	if err != nil {
		return m, err
	}
	for _, line := range strings.Split(keyspace, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "db") {
			return m, fmt.Errorf("restore requires empty Redis target")
		}
	}
	aof, err := r.output(ctx, "exec", "-T", "-e", "REDISCLI_AUTH", "redis", "redis-cli", "-e", "--raw", "CONFIG", "GET", "appendonly")
	if err != nil || strings.TrimSpace(aof) != "appendonly\nno" {
		return m, fmt.Errorf("restore target must have appendonly disabled")
	}
	dump, err := os.Open(filepath.Join(scratch, "payload", "mysql.sql")) // #nosec G304 -- scratch dir is created by this restore run inside the private state dir.
	if err != nil {
		return m, err
	}
	defer dump.Close()
	if err = r.compose(ctx, io.Discard, dump, "exec", "-T", "mysql", "sh", "-c", `exec env MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot`); err != nil {
		return m, fmt.Errorf("restore SQL import failed; discard isolated target")
	}
	if err = r.compose(ctx, io.Discard, nil, "stop", "--timeout", "60", "redis"); err != nil {
		return m, err
	}
	cid, err := r.output(ctx, "ps", "--all", "-q", "redis")
	if err != nil {
		return m, err
	}
	if err = r.command(ctx, io.Discard, nil, "cp", filepath.Join(scratch, "payload", "redis.rdb"), cid+":/data/dump.rdb"); err != nil {
		return m, err
	}
	if err = r.compose(ctx, io.Discard, nil, "start", "--wait", "--wait-timeout", "120", "redis"); err != nil {
		return m, err
	}
	// Configuration is recovered to a private review directory, never applied
	// to the target automatically (which could reconnect it to production).
	out := filepath.Join(r.Config.StateDir, "recovered-"+m.JobID)
	if !ident.MatchString(m.JobID) {
		return m, fmt.Errorf("invalid manifest job id")
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return m, err
	}
	for _, f := range m.Files {
		if !strings.HasPrefix(f.Name, "config/") {
			continue
		}
		if !safeName(f.Name) {
			return m, fmt.Errorf("invalid manifest file name")
		}
		dest := filepath.Join(out, f.Name)
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return m, err
		}
		raw, e := os.ReadFile(filepath.Join(scratch, "payload", f.Name)) // #nosec G304 -- payload file restored from the sha-verified archive into this run's scratch dir.
		if e != nil {
			return m, e
		}
		if err = os.WriteFile(dest, raw, 0600); err != nil { // #nosec G703 -- f.Name passed safeName; dest cannot escape out.
			return m, err
		}
	}
	return m, nil
}
