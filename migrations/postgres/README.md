# Postgres Migrations

This directory holds the Postgres-flavored migrations used by the
Postgres deployment mode (see `docs/deployment.md` §9.2).

A sibling `migrations/sqlite/` directory contains the SQLite3
equivalent (Lite deployment, §9.1). The two snapshots are maintained
in lockstep with the MySQL `migrations/` directory.

## Layout

- `000_create_full_schema.sql` — single baseline that creates every
  table the application needs on a fresh Postgres database. It is a
  hand-written snapshot of the MySQL schema (the union of
  `migrations/000_…038_…sql`) translated to Postgres-compatible syntax.

## Why a single baseline file?

The MySQL migrations rely on a number of MySQL-only features that
don't have a direct Postgres equivalent (`AUTO_INCREMENT` → `BIGSERIAL`,
`ENGINE=InnoDB`, `DEFAULT CHARSET`, inline `COMMENT`, `MODIFY COLUMN`,
`ON UPDATE CURRENT_TIMESTAMP`, `PARTITION BY RANGE`). Translating them
verbatim would either lose MySQL capabilities or require a
conditional-SQL runner. A hand-written snapshot is much easier to keep
aligned and review.

## Running

```sh
MIGRATIONS_DRIVER=postgres \
MIGRATIONS_DSN='host=127.0.0.1 user=app password=… dbname=micro_one_api port=5432 sslmode=disable' \
go run ./cmd/migrate -dir ./migrations/postgres
```

The driver is auto-detected from the DSN, so `MIGRATIONS_DRIVER` can
be omitted when the DSN starts with `postgres://` / `postgresql://` or
uses the `host=…` key=value form.

## Notes for contributors

- Keep this directory in sync with `migrations/sqlite/` and
  `migrations/` (MySQL). CI runs `cmd/migrate -dir ./migrations/<dialect>`
  against scratch databases on every PR.
- Use `BIGSERIAL PRIMARY KEY` for auto-incrementing ids (not
  `AUTO_INCREMENT`).
- Use `TEXT` for variable-length strings; use `VARCHAR(N)` only when
  an index requires a length cap.
- Use `BOOLEAN` for booleans; `BIGINT` for timestamps stored as
  epoch seconds/milliseconds (matching the application's time.Time → int
  encoding).
- Foreign keys are enforced by default; no PRAGMA is required.
- Do not introduce MySQL-only DDL (PARTITION BY, ENGINE=, COMMENT, etc.).
