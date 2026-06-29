# SQLite3 Migrations

This directory holds the SQLite3-flavored migrations used by the Lite
deployment mode (see `docs/deployment.md` §9.1 and
`docs/issue-4-sqlite-solution.md`).

A sibling `migrations/postgres/` directory contains the Postgres
equivalent (see `docs/deployment.md` §9.2). The two snapshots are
maintained in lockstep: any schema change should add a new numbered
file in **both** this directory and the corresponding Postgres
directory (and in `migrations/` for MySQL).

## Layout

- `000_create_full_schema.sql` — single baseline that creates every
  table the application needs on a fresh SQLite3 database. It is a
  hand-written snapshot of the MySQL schema (the union of
  `migrations/000_…038_…sql`) translated to SQLite3-compatible syntax.

## Why a single baseline file?

The MySQL migrations rely on a number of MySQL-only features that
don't have a direct SQLite3 equivalent (`AUTO_INCREMENT`,
`ENGINE=InnoDB`, `DEFAULT CHARSET`, `COMMENT`, `MODIFY COLUMN`, prefix
indexes, `ON UPDATE CURRENT_TIMESTAMP`, `PARTITION BY RANGE`).
Translating them verbatim would either lose MySQL capabilities or
require a conditional-SQL runner. A hand-written snapshot is much
easier to keep aligned and review.

## Running

```sh
MIGRATIONS_DRIVER=sqlite3 \
MIGRATIONS_DSN='file:/data/micro-one-api.db?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on' \
go run ./cmd/migrate -dir ./migrations/sqlite
```

The driver is auto-detected from the DSN, so `MIGRATIONS_DRIVER` can
be omitted when the DSN starts with `file:` or ends with `.db` /
`.sqlite` / `.sqlite3`.

## Notes for contributors

- Keep this directory in sync with `migrations/postgres/` and
  `migrations/` (MySQL). CI runs `cmd/migrate -dir ./migrations/<dialect>`
  against scratch databases on every PR.
- Use `INTEGER PRIMARY KEY AUTOINCREMENT` (not `AUTO_INCREMENT`) for ids.
- Use `TEXT` for variable-length strings; avoid `VARCHAR(N)`.
- Foreign keys must be enabled per-connection (the runner sets
  `PRAGMA foreign_keys = ON`).
- Do not introduce MySQL-only DDL (PARTITION BY, ENGINE=, COMMENT, etc.).
