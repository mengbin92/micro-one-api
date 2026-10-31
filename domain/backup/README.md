# Managed MySQL + Redis backups

`biz` owns policy, receipts and the repository port. `data` owns SQL persistence,
S3, age archives and fixed Docker Compose orchestration. Admin is the control
owner; `cmd/backup-worker` is a private host role. No Docker socket, snapshot
credential, S3 credential or decryption identity enters admin-api.

Only MySQL and a standalone primary Redis at `/data/dump.rdb` are supported.
Portable control tables do not imply PostgreSQL/SQLite backup support. The
host worker uses a process file lock plus a MySQL connection leader lock. All
instances for a project must share the same private state directory.

See [the frozen B3 contract](../../docs/design/b3-managed-backup-contract-2026-10-30.md)
and [acceptance and recovery instructions](../../docs/runbooks/b3-managed-backup-acceptance-2026-10-30.md).
