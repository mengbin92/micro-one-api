package data

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"filippo.io/age"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/pkg/safefile"
)

var Writers = []string{"relay-gateway", "admin-api", "identity-service", "channel-service", "billing-service", "config-service", "log-service", "monitor-worker", "notify-worker"}
var ident = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,79}$`)

type Config struct {
	ComposeFile      string            `json:"compose_file"`
	Project          string            `json:"project"`
	StateDir         string            `json:"state_dir"`
	ControlDSNEnv    string            `json:"control_dsn_env"`
	SnapshotDSNEnv   string            `json:"snapshot_dsn_env"`
	RedisPasswordEnv string            `json:"redis_password_env"`
	Schemas          []string          `json:"schemas"`
	Files            map[string]string `json:"files"`
	KeyReferences    []string          `json:"key_references"`
	SourceRevision   string            `json:"source_revision"`
	Recipient        string            `json:"age_recipient"`
	S3               S3Config          `json:"s3"`
	TimeoutMinutes   int               `json:"timeout_minutes"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	raw, err := safefile.ReadFile(path) // path is the operator-supplied worker config.
	if err != nil {
		return c, err
	}
	dec := jsonx.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if !filepath.IsAbs(c.ComposeFile) || !filepath.IsAbs(c.StateDir) || !ident.MatchString(c.Project) || c.ControlDSNEnv == "" || c.SnapshotDSNEnv == "" || c.RedisPasswordEnv == "" || c.SourceRevision == "" || len(c.Schemas) == 0 || c.TimeoutMinutes < 1 || c.TimeoutMinutes > 60 {
		return fmt.Errorf("explicit backup topology, version, credential references and timeout required")
	}
	if _, err := age.ParseX25519Recipient(c.Recipient); err != nil {
		return fmt.Errorf("valid offline age public recipient required")
	}
	seen := map[string]bool{}
	for _, s := range c.Schemas {
		if !ident.MatchString(s) || strings.Contains(s, "-") || seen[s] || s == "mysql" || s == "sys" || s == "information_schema" || s == "performance_schema" {
			return fmt.Errorf("invalid or duplicate application schema")
		}
		seen[s] = true
	}
	for _, key := range []string{"JWT_SECRET_KEY", "CHANNEL_ENCRYPTION_KEY", "IDENTITY_MFA_ENCRYPTION_KEY"} {
		if !slices.Contains(c.KeyReferences, key) {
			return fmt.Errorf("recovery key references incomplete")
		}
	}
	if len(c.Files) == 0 {
		return fmt.Errorf("explicit recovery configuration files required")
	}
	for name, file := range c.Files {
		if !safeName(name) || (name == "compose.json" || name == "worker-config.json") || !filepath.IsAbs(file) {
			return fmt.Errorf("invalid recovery file reference")
		}
	}
	return nil
}

type Runner struct{ Config Config }

func (r *Runner) command(ctx context.Context, output io.Writer, input io.Reader, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...) // #nosec G204 -- fixed docker argv template, no shell; container names come from operator config.
	cmd.Stdout = output
	cmd.Stdin = input
	cmd.Env = append(os.Environ(), "REDISCLI_AUTH="+os.Getenv(r.Config.RedisPasswordEnv))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker backup operation failed: %w", err)
	}
	return nil
}
func (r *Runner) compose(ctx context.Context, out io.Writer, in io.Reader, args ...string) error {
	base := []string{"compose", "-f", r.Config.ComposeFile, "-p", r.Config.Project}
	return r.command(ctx, out, in, append(base, args...)...)
}
func (r *Runner) output(ctx context.Context, args ...string) (string, error) {
	var out bytes.Buffer
	err := r.compose(ctx, &out, nil, args...)
	return strings.TrimSpace(out.String()), err
}

type journal struct {
	Project     string   `json:"project"`
	ComposeFile string   `json:"compose_file"`
	Services    []string `json:"services"`
}

func (r *Runner) journalPath() string { return filepath.Join(r.Config.StateDir, "resume.json") }
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private state directory (0700) required")
	}
	return nil
}
func syncDir(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- dir is a scratch/state directory created by the worker itself.
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (r *Runner) saveJournal(services []string) error {
	raw, _ := jsonx.Marshal(journal{r.Config.Project, r.Config.ComposeFile, services})
	f, err := os.OpenFile(r.journalPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return syncDir(r.Config.StateDir)
}
func (r *Runner) Recover(ctx context.Context) error {
	raw, err := os.ReadFile(r.journalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j journal
	if err = jsonx.Unmarshal(raw, &j); err != nil {
		return err
	}
	if j.Project != r.Config.Project || j.ComposeFile != r.Config.ComposeFile {
		return fmt.Errorf("resume journal topology mismatch")
	}
	for _, s := range j.Services {
		if s != "redis" && !slices.Contains(Writers, s) {
			return fmt.Errorf("unsafe resume journal")
		}
	}
	if len(j.Services) > 0 {
		args := append([]string{"start", "--wait", "--wait-timeout", "120"}, j.Services...)
		if err = r.compose(ctx, io.Discard, nil, args...); err != nil {
			return err
		}
	}
	if err = os.Remove(r.journalPath()); err != nil {
		return err
	}
	return syncDir(r.Config.StateDir)
}
func (r *Runner) schemas(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema')")
	if err != nil {
		return err
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return err
		}
		actual = append(actual, name)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	wanted := slices.Clone(r.Config.Schemas)
	slices.Sort(wanted)
	slices.Sort(actual)
	if !slices.Equal(actual, wanted) {
		return fmt.Errorf("configured schemas do not cover the complete MySQL instance")
	}
	return nil
}
func (r *Runner) Snapshot(ctx context.Context, id, dir string) (m Manifest, files map[string]string, err error) {
	c := r.Config
	m = Manifest{Format: 1, Project: c.Project, JobID: id, SourceRevision: c.SourceRevision, Schemas: c.Schemas, KeyReferences: c.KeyReferences, Images: map[string]string{}}
	files = map[string]string{}
	m.KeyReferences = slices.Clone(c.KeyReferences)
	for _, ref := range []string{c.ControlDSNEnv, c.SnapshotDSNEnv, c.RedisPasswordEnv, c.S3.AccessKeyEnv, c.S3.SecretKeyEnv} {
		if ref != "" && !slices.Contains(m.KeyReferences, ref) {
			m.KeyReferences = append(m.KeyReferences, ref)
		}
	}
	if err = privateDir(c.StateDir); err != nil {
		return
	}
	if _, e := os.Stat(r.journalPath()); !errors.Is(e, os.ErrNotExist) {
		err = fmt.Errorf("unrecovered backup journal; run recover first")
		return
	}
	// Validate the configured Compose project, including unexpected store writers.
	rendered, e := r.output(ctx, "config", "--format", "json")
	if e != nil {
		err = e
		return
	}
	var topology struct {
		Services map[string]struct{ Environment map[string]any }
	}
	if e = jsonx.Unmarshal([]byte(rendered), &topology); e != nil {
		err = e
		return
	}
	for name, s := range topology.Services {
		if slices.Contains(Writers, name) || name == "mysql" || name == "redis" || name == "migrate" {
			continue
		}
		for key := range s.Environment {
			if strings.Contains(key, "DSN") || strings.HasPrefix(key, "REDIS_") {
				err = fmt.Errorf("unreviewed Compose store writer")
				return
			}
		}
	}
	running, e := r.output(ctx, "ps", "--status", "running", "--services")
	if e != nil {
		err = e
		return
	}
	active := strings.Fields(running)
	if !slices.Contains(active, "mysql") || !slices.Contains(active, "redis") {
		err = fmt.Errorf("MySQL and Redis must be running")
		return
	}
	if slices.Contains(active, "migrate") {
		err = fmt.Errorf("active schema migration must finish before backup")
		return
	}
	if err = r.verifyRedis(ctx); err != nil {
		return
	}
	var stopped []string
	for _, name := range active {
		if slices.Contains(Writers, name) {
			stopped = append(stopped, name)
		}
	}
	// Images and fully resolved configuration are encrypted with the data.
	for _, name := range append(slices.Clone(stopped), "mysql", "redis") {
		cid, e := r.output(ctx, "ps", "-q", name)
		if e != nil {
			err = e
			return
		}
		var out bytes.Buffer
		if e = r.command(ctx, &out, nil, "inspect", "--format", "{{.Image}}", cid); e != nil {
			err = e
			return
		}
		m.Images[name] = strings.TrimSpace(out.String())
	}
	cp := filepath.Join(dir, "compose.json")
	if err = os.WriteFile(cp, []byte(rendered), 0600); err != nil {
		return
	}
	files["config/compose.json"] = cp
	workerConfig, e := jsonx.Marshal(c)
	if e != nil {
		err = e
		return
	}
	workerPath := filepath.Join(dir, "worker-config.json")
	if err = os.WriteFile(workerPath, workerConfig, 0600); err != nil {
		return
	}
	files["config/worker-config.json"] = workerPath
	for name, file := range c.Files {
		info, e := os.Lstat(file)
		if e != nil || !info.Mode().IsRegular() {
			err = fmt.Errorf("recovery config must be a regular file")
			return
		}
		dest := filepath.Join(dir, "config", name)
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return
		}
		in, e := os.Open(file) // #nosec G304 -- file comes from walking the operator-configured config/state inventory dir.
		if e != nil {
			err = e
			return
		}
		out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- dest is inventory-rooted inside the worker-created state dir.
		if e != nil {
			in.Close()
			err = e
			return
		}
		_, err = io.Copy(out, in)
		in.Close()
		ce := out.Close()
		if err != nil {
			return
		}
		if ce != nil {
			err = ce
			return
		}
		files["config/"+name] = dest
	}
	db, e := sql.Open("mysql", os.Getenv(c.SnapshotDSNEnv))
	if e != nil {
		err = e
		return
	}
	defer db.Close()
	if err = r.verifyMySQL(ctx, db, true); err != nil {
		return
	}
	if err = r.schemas(ctx, db); err != nil {
		return
	}
	if err = r.saveJournal(append(slices.Clone(stopped), "redis")); err != nil {
		return
	}
	// Resume on every ordinary failure and cancellation. SIGKILL/power loss is
	// recovered from the fsynced journal under the same MySQL leader lock.
	defer func() {
		resume, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		if e := r.Recover(resume); e != nil {
			err = errors.Join(err, fmt.Errorf("services_resume_failed: %w", e))
		}
	}()
	if len(stopped) > 0 {
		if err = r.compose(ctx, io.Discard, nil, append([]string{"stop", "--timeout", "60"}, stopped...)...); err != nil {
			return
		}
	}
	conn, e := db.Conn(ctx)
	if e != nil {
		err = e
		return
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "FLUSH TABLES WITH READ LOCK"); err != nil {
		return
	}
	defer func() {
		unlock, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, e := conn.ExecContext(unlock, "UNLOCK TABLES")
		err = errors.Join(err, e)
	}()
	if err = r.compose(ctx, io.Discard, nil, "exec", "-T", "-e", "REDISCLI_AUTH", "redis", "redis-cli", "-e", "SAVE"); err != nil {
		return
	}
	if err = r.compose(ctx, io.Discard, nil, "stop", "--timeout", "60", "redis"); err != nil {
		return
	}
	m.SnapshotAt = time.Now().Unix()
	dump, e := os.OpenFile(filepath.Join(dir, "mysql.sql"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- dir is the runner-created snapshot staging dir.
	if e != nil {
		err = e
		return
	}
	args := []string{"exec", "-T", "mysql", "sh", "-c", `exec env MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -uroot --single-transaction --quick --skip-lock-tables --no-tablespaces --set-gtid-purged=OFF --routines --events --triggers --databases "$@"`, "backup"}
	args = append(args, c.Schemas...)
	err = r.compose(ctx, dump, nil, args...)
	closeErr := dump.Close()
	if err != nil {
		return
	}
	if closeErr != nil {
		err = closeErr
		return
	}
	files["mysql.sql"] = dump.Name()
	cid, e := r.output(ctx, "ps", "--all", "-q", "redis")
	if e != nil {
		err = e
		return
	}
	rdb := filepath.Join(dir, "redis.rdb")
	if err = r.command(ctx, io.Discard, nil, "cp", cid+":/data/dump.rdb", rdb); err != nil {
		return
	}
	if err = os.Chmod(rdb, 0600); err != nil {
		return
	}
	files["redis.rdb"] = rdb
	// Prove the lock connection is still alive before declaring consistency.
	var alive int
	if err = conn.QueryRowContext(ctx, "SELECT 1").Scan(&alive); err != nil {
		return
	}
	return
}

// The lock, control receipts and Compose dump must refer to the same server.
// Otherwise a valid snapshot could be stamped against an unrelated data set.
func (r *Runner) verifyMySQL(ctx context.Context, db *sql.DB, control bool) error {
	var source string
	if err := db.QueryRowContext(ctx, "SELECT @@server_uuid").Scan(&source); err != nil {
		return err
	}
	actual, err := r.output(ctx, "exec", "-T", "mysql", "sh", "-c", `exec env MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B -e 'SELECT @@server_uuid'`)
	if err != nil || actual != source {
		return fmt.Errorf("Compose and snapshot MySQL server mismatch")
	}
	if control {
		dsn := os.Getenv(r.Config.ControlDSNEnv)
		if dsn == "" {
			return fmt.Errorf("control DB reference missing")
		}
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		var other string
		if err = db.QueryRowContext(ctx, "SELECT @@server_uuid").Scan(&other); err != nil || other != source {
			return fmt.Errorf("control and snapshot MySQL server mismatch")
		}
	}
	return nil
}

func (r *Runner) verifyRedis(ctx context.Context) error {
	for _, field := range []struct{ key, value string }{{"dir", "/data"}, {"dbfilename", "dump.rdb"}} {
		out, err := r.output(ctx, "exec", "-T", "-e", "REDISCLI_AUTH", "redis", "redis-cli", "-e", "--raw", "CONFIG", "GET", field.key)
		if err != nil || out != field.key+"\n"+field.value {
			return fmt.Errorf("Redis requires explicit standalone /data/dump.rdb topology")
		}
	}
	for _, check := range []struct{ section, expected string }{{"cluster", "cluster_enabled:0"}, {"replication", "role:master"}} {
		out, err := r.output(ctx, "exec", "-T", "-e", "REDISCLI_AUTH", "redis", "redis-cli", "-e", "--raw", "INFO", check.section)
		if err != nil || !strings.Contains(out, check.expected) {
			return fmt.Errorf("Redis standalone primary required")
		}
	}
	return nil
}
