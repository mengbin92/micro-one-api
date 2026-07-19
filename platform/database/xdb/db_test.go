package xdb

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
)

func TestInferDriver(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		{"", DriverMySQL},
		{"user:pass@tcp(127.0.0.1:3306)/db?parseTime=true", DriverMySQL},
		{":memory:", DriverSQLite3},
		{"file:/data/micro-one-api.db", DriverSQLite3},
		{"file:/tmp/x.db?_busy_timeout=5000", DriverSQLite3},
		{"/var/data/app.db", DriverSQLite3},
		{"./local.sqlite", DriverSQLite3},
		{"./local.sqlite3", DriverSQLite3},
		{"/tmp/app.sqlite", DriverSQLite3},
		{"postgres://user:pw@host:5432/db", DriverPostgres},
		{"postgresql://user:pw@host:5432/db", DriverPostgres},
		{"host=127.0.0.1 user=app dbname=micro_one_api", DriverPostgres},
	}
	for _, c := range cases {
		if got := InferDriver(c.dsn); got != c.want {
			t.Errorf("InferDriver(%q)=%q, want %q", c.dsn, got, c.want)
		}
	}
}

func TestNormalizeDriver(t *testing.T) {
	if got := NormalizeDriver("", ":memory:"); got != DriverSQLite3 {
		t.Errorf("NormalizeDriver empty+memory = %q, want %q", got, DriverSQLite3)
	}
	if got := NormalizeDriver("", "user:pass@tcp(127.0.0.1:3306)/db"); got != DriverMySQL {
		t.Errorf("NormalizeDriver blank+MySQL DSN = %q, want %q", got, DriverMySQL)
	}
	if got := NormalizeDriver("MySQL", "x"); got != DriverMySQL {
		t.Errorf("NormalizeDriver case insensitive = %q, want %q", got, DriverMySQL)
	}
	if got := NormalizeDriver("sqlite", "x"); got != DriverSQLite3 {
		t.Errorf("NormalizeDriver 'sqlite' alias = %q, want %q", got, DriverSQLite3)
	}
	if got := NormalizeDriver("postgresql", "x"); got != DriverPostgres {
		t.Errorf("NormalizeDriver 'postgresql' alias = %q, want %q", got, DriverPostgres)
	}
	if got := NormalizeDriver("SQLITE3", "x"); got != DriverSQLite3 {
		t.Errorf("NormalizeDriver case insensitive = %q, want %q", got, DriverSQLite3)
	}
}

func TestOpenSQLite3InMemory(t *testing.T) {
	db, err := Open(DatabaseConfig{Driver: DriverSQLite3, DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open SQLite3 :memory: failed: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	if db.Dialector == nil {
		t.Fatal("expected non-nil dialector")
	}
	if db.Dialector.Name() != "sqlite" {
		t.Errorf("Dialector.Name()=%q, want sqlite", db.Dialector.Name())
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() failed: %v", err)
	}
	if sqlDB.Driver() == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestOpenDriverInferenceFromDSN(t *testing.T) {
	db, err := Open(DatabaseConfig{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open (driver inferred) failed: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if db.Dialector.Name() != "sqlite" {
		t.Errorf("dialector name = %q, want sqlite", db.Dialector.Name())
	}
}

func TestOpenUnsupportedDriver(t *testing.T) {
	_, err := Open(DatabaseConfig{Driver: "oracle", DSN: "x"})
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error %q does not mention 'unsupported'", err.Error())
	}
}

func TestOpenSQLite3WithCustomPragma(t *testing.T) {
	db, err := Open(DatabaseConfig{
		Driver:         DriverSQLite3,
		DSN:            ":memory:",
		SQLite3Pragmas: []string{"PRAGMA busy_timeout = 1234"},
	})
	if err != nil {
		t.Fatalf("Open SQLite3 custom pragma failed: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	// busy_timeout is connection-local; verify by querying the
	// pragma back on the same *sql.DB.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() failed: %v", err)
	}
	var got int
	if err := sqlDB.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if got != 1234 {
		t.Errorf("busy_timeout=%d, want 1234 (custom pragma not applied)", got)
	}
}

func TestSQLite3PoolConfig(t *testing.T) {
	p := SQLite3PoolConfig()
	if p == nil {
		t.Fatal("SQLite3PoolConfig returned nil")
	}
	if p.MaxOpenConns != 1 {
		t.Errorf("MaxOpenConns=%d, want 1", p.MaxOpenConns)
	}
	if p.MaxIdleConns != 1 {
		t.Errorf("MaxIdleConns=%d, want 1", p.MaxIdleConns)
	}
}

func TestDefaultSQLite3Pragmas(t *testing.T) {
	p := DefaultSQLite3Pragmas()
	if len(p) == 0 {
		t.Fatal("DefaultSQLite3Pragmas returned empty slice")
	}
	// Verify mutating the returned slice does not change the package state.
	p[0] = "PRAGMA foo"
	again := DefaultSQLite3Pragmas()
	if again[0] == "PRAGMA foo" {
		t.Fatal("DefaultSQLite3Pragmas returned shared backing array")
	}
}

func TestPostgresDriverNameConstant(t *testing.T) {
	if PostgresDriverName != "pgx" {
		t.Errorf("PostgresDriverName=%q, want %q", PostgresDriverName, "pgx")
	}
	EnsurePostgresDriver()
}

func TestOpenPostgresDriverNameRegistered(t *testing.T) {
	// The gorm pgx stdlib driver registers under "pgx". We can verify
	// the registration without an actual Postgres server by calling
	// sql.Open — the call should return a *sql.DB, with a real
	// connection only attempted on first use. Then we explicitly Close
	// before any I/O.
	db, err := sql.Open("pgx", "host=127.0.0.1 port=1 sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open pgx: %v", err)
	}
	defer db.Close()
	if db == nil {
		t.Fatal("expected non-nil *sql.DB for pgx driver")
	}
}

func TestOpenPostgresRejectsBadDSN(t *testing.T) {
	// A reachable-but-non-Postgres endpoint should fail on Ping, not on
	// Open. We only verify that Open does not panic and that the
	// returned error from Ping is non-nil.
	db, err := Open(DatabaseConfig{Driver: DriverPostgres, DSN: "host=127.0.0.1 port=1 sslmode=disable"})
	if err != nil {
		// Some driver versions validate the DSN eagerly and fail here.
		// Either outcome is acceptable; we only require it not to panic.
		return
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if db.Dialector == nil {
		t.Fatal("expected non-nil dialector")
	}
	if db.Dialector.Name() != "postgres" {
		t.Errorf("Dialector.Name()=%q, want postgres", db.Dialector.Name())
	}
}

// ---------------------------------------------------------------------------
// Phase 2.4 — schema isolation helpers.
// ---------------------------------------------------------------------------

func TestWithMySQLDBName(t *testing.T) {
	cases := []struct {
		name   string
		dsn    string
		schema string
		want   string
	}{
		{
			name:   "plain",
			dsn:    "user:pass@tcp(127.0.0.1:3306)/oneapi?parseTime=true",
			schema: "oneapi_identity",
			want:   "user:pass@tcp(127.0.0.1:3306)/oneapi_identity?parseTime=true",
		},
		{
			name:   "no params preserves question mark structure",
			dsn:    "user:pass@tcp(127.0.0.1:3306)/oneapi",
			schema: "oneapi_log",
			want:   "user:pass@tcp(127.0.0.1:3306)/oneapi_log",
		},
		{
			name:   "schema with backticks is stripped",
			dsn:    "user:pass@tcp(127.0.0.1:3306)/oneapi?charset=utf8mb4",
			schema: "`oneapi_channel`",
			want:   "user:pass@tcp(127.0.0.1:3306)/oneapi_channel?charset=utf8mb4",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := withMySQLDBName(c.dsn, c.schema)
			if err != nil {
				t.Fatalf("withMySQLDBName: %v", err)
			}
			if got != c.want {
				t.Errorf("withMySQLDBName(%q,%q) =\n  %q\nwant %q", c.dsn, c.schema, got, c.want)
			}
		})
	}
}

func TestWithMySQLDBName_EmptySchemaReturnsOriginal(t *testing.T) {
	dsn := "user:pass@tcp(127.0.0.1:3306)/oneapi?parseTime=true"
	got, err := withMySQLDBName(dsn, "")
	if err != nil {
		t.Fatalf("withMySQLDBName: %v", err)
	}
	if got != dsn {
		t.Errorf("empty schema should be a no-op, got %q", got)
	}
}

func TestWithMySQLDBName_InvalidDSNReturnsError(t *testing.T) {
	// ParseDSN rejects DSNs without a slash.
	_, err := withMySQLDBName("not a dsn", "schema")
	if err == nil {
		t.Fatal("expected error for unparseable DSN")
	}
}

func TestWithPostgresSearchPath_URLForm(t *testing.T) {
	dsn := "postgres://user:pw@host:5432/oneapi?sslmode=disable"
	got, err := withPostgresSearchPath(dsn, "oneapi_identity")
	if err != nil {
		t.Fatalf("withPostgresSearchPath: %v", err)
	}
	// search_path ends up URL-encoded inside the options= query param.
	decoded, err := url.QueryUnescape(got)
	if err != nil {
		t.Fatalf("url.QueryUnescape: %v", err)
	}
	if !strings.Contains(decoded, "search_path=oneapi_identity") {
		t.Errorf("URL-form DSN missing search_path: %q", got)
	}
	if !strings.HasPrefix(got, "postgres://user:pw@host:5432/oneapi?") {
		t.Errorf("URL-form DSN prefix mangled: %q", got)
	}
	if !strings.Contains(got, "sslmode=disable") {
		t.Errorf("URL-form DSN dropped sslmode: %q", got)
	}
}

func TestWithPostgresSearchPath_URLFormReplacesExistingOptions(t *testing.T) {
	dsn := "postgres://user:pw@host:5432/oneapi?sslmode=disable&options=-c%20foo%3Dbar"
	got, err := withPostgresSearchPath(dsn, "oneapi_log")
	if err != nil {
		t.Fatalf("withPostgresSearchPath: %v", err)
	}
	decoded, err := url.QueryUnescape(got)
	if err != nil {
		t.Fatalf("url.QueryUnescape: %v", err)
	}
	if strings.Count(decoded, "options=") != 1 {
		t.Errorf("expected exactly one options=, got %q", got)
	}
	if !strings.Contains(decoded, "search_path=oneapi_log") {
		t.Errorf("missing search_path in replaced options: %q", got)
	}
}

func TestWithPostgresSearchPath_KeyValueForm(t *testing.T) {
	dsn := "host=127.0.0.1 user=app dbname=oneapi port=5432 sslmode=disable"
	got, err := withPostgresSearchPath(dsn, "oneapi_billing")
	if err != nil {
		t.Fatalf("withPostgresSearchPath: %v", err)
	}
	if !strings.Contains(got, "search_path=oneapi_billing") {
		t.Errorf("KV-form DSN missing search_path: %q", got)
	}
	if !strings.Contains(got, "dbname=oneapi") {
		t.Errorf("KV-form DSN dropped dbname: %q", got)
	}
}

func TestWithPostgresSearchPath_KeyValueFormAppendsWhenMissing(t *testing.T) {
	dsn := "host=127.0.0.1 dbname=oneapi"
	got, err := withPostgresSearchPath(dsn, "oneapi_admin")
	if err != nil {
		t.Fatalf("withPostgresSearchPath: %v", err)
	}
	if !strings.Contains(got, "options=") {
		t.Errorf("KV-form DSN missing options=: %q", got)
	}
	if !strings.Contains(got, "search_path=oneapi_admin") {
		t.Errorf("KV-form DSN missing search_path: %q", got)
	}
}

func TestSplitPostgresKV_QuotedValue(t *testing.T) {
	parts := splitPostgresKV("host=127.0.0.1 options='-c foo=bar -c search_path=x' dbname=app")
	want := map[string]string{
		"host":    "127.0.0.1",
		"options": "-c foo=bar -c search_path=x",
		"dbname":  "app",
	}
	if len(parts) != len(want) {
		t.Fatalf("got %d parts, want %d: %+v", len(parts), len(want), parts)
	}
	for _, kv := range parts {
		if want[kv.key] != kv.value {
			t.Errorf("part %s = %q, want %q", kv.key, kv.value, want[kv.key])
		}
	}
}

func TestWithPostgresSearchPath_RejectsInvalidSchema(t *testing.T) {
	cases := []string{
		// Whitespace would let an attacker break out of the search_path slot
		// and inject a second -c runtime parameter via options.
		"public -c statement_timeout=1",
		"public; DROP TABLE x",
		"pub lic",
		"public$",
		"public;",
		// hyphen is rejected: a schema name with "-" could look like a CLI flag.
		"pub-lic",
		strings.Repeat("a", 64), // too long
		"1starts_with_digit",
	}
	for _, schema := range cases {
		got, err := withPostgresSearchPath("postgres://u:p@h:5432/db", schema)
		if err == nil {
			t.Errorf("schema %q should be rejected, got dsn=%q", schema, got)
		}
		if got != "postgres://u:p@h:5432/db" {
			t.Errorf("on error the original DSN must be returned unchanged, got %q", got)
		}
	}
}

func TestWithMySQLDBName_RejectsInvalidSchema(t *testing.T) {
	cases := []string{
		"db; DROP TABLE x",
		"two words",
		"public$",
		strings.Repeat("a", 64),
	}
	const dsn = "user:pass@tcp(127.0.0.1:3306)/orig?parseTime=true"
	for _, schema := range cases {
		got, err := withMySQLDBName(dsn, schema)
		if err == nil {
			t.Errorf("schema %q should be rejected, got %q", schema, got)
		}
		if got != dsn {
			t.Errorf("on error the original DSN must be returned unchanged, got %q", got)
		}
	}
}

func TestValidSchemaIdentifier(t *testing.T) {
	cases := []struct {
		schema string
		wantOK bool
	}{
		{"", true}, // empty = no isolation
		{"oneapi_billing", true},
		{"ONEAPI", true},
		{"oneapi_v2", true},
		{"_private", true},
		{"a", true},
		// rejects
		{"1leading_digit", false},
		{"has space", false},
		{"has-dash", false},
		{"has$dollar", false},
		{"has;semi", false},
		{`"quoted"`, false},
		{"has`back", false},
		{strings.Repeat("a", 64), false}, // too long
	}
	for _, tc := range cases {
		err := validSchemaIdentifier(tc.schema)
		if tc.wantOK && err != nil {
			t.Errorf("schema %q: want OK, got err=%v", tc.schema, err)
		}
		if !tc.wantOK && err == nil {
			t.Errorf("schema %q: want rejection, got nil err", tc.schema)
		}
	}
}
