# Bounded operator tasks

This shared library owns only the operator SQLite queue and immutable task
receipts. It never opens a business database or changes user finances. `biz`
declares plans, claims and the task port; `data` holds the SQLite client and
implements cross-process claims. It is used by the billing command's isolated
cost / repair / weekly roles, not by the relay hot path.

Plans are fixed by identity and payload SHA. Budget is consumed before sending
an external request. An expired running claim becomes `uncertain` and cannot
be retried automatically. A repair plan has one run, independent of clock slots.
Late provider costs require another independently reviewed CSV/reference; they
cannot silently change the fixed report scope or become invoice approval.

The generation client uses no tools or filesystem execution. It sends only
reviewed schema/code plus redacted numeric usage. The repair role rejects
business DSNs, service/operator tokens and reviewer configuration. The operator
Compose profile adds read-only filesystem, input mounts, an unprivileged user,
no Linux capabilities, a private state mount and resource limits. Candidate
output still requires independent fixtures, human review, CI and owner gates.
