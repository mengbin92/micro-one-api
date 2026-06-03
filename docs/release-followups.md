# Release Follow-ups

Generated on 2026-06-03 from local release validation.

## Verification Status

Passed:

- `npm run lint`
- `npm run test`
- `make test`
- `make test-e2e-suite`
- `make build`
- `govulncheck ./...`
- `/tmp/gitleaks detect --source . --verbose --no-banner --config .gitleaks.toml`

Known remaining security scan findings:

- `gosec`: 29 high, 12 medium, 120 low findings.
- The current GitHub workflow runs `gosec` with `-no-fail` and uploads SARIF, so these are tracked follow-ups rather than current CI blockers.

Reproduce the gosec report:

```bash
gosec -fmt json -out /tmp/micro-one-api-gosec.json $(go list -f '{{.Dir}}' ./... | grep -v '/web/node_modules/')
```

## High Priority

- `G115` `internal/channel/data/data.go:554`: integer overflow conversion `uint -> uint32`.
- `G115` `internal/billing/data/payment_repo.go:234`: integer overflow conversion `uint -> int64`.
- `G115` `internal/billing/data/payment_repo.go:213`: integer overflow conversion `int64 -> uint`.
- `G115` `internal/admin/server/http.go:917`: integer overflow conversion `int64 -> int32`.
- `G115` `internal/billing/data/redeem_repo.go:50`: integer overflow conversion `int32 -> int8`.
- `G115` `internal/billing/data/redeem_repo.go:28`: integer overflow conversion `int32 -> int8`.
- `G115` `internal/relay/service/relay.go:218`: integer overflow conversion `int -> int32`.
- `G115` `internal/relay/service/relay.go:213`: integer overflow conversion `int -> int32`.
- `G115` `internal/relay/service/relay.go:212`: integer overflow conversion `int -> int32`.
- `G115` `internal/relay/service/relay.go:211`: integer overflow conversion `int -> int32`.
- `G115` `internal/notify/service/notify.go:83`: integer overflow conversion `int -> int32`.
- `G115` `internal/monitor/service/monitor.go:154`: integer overflow conversion `int -> int32`.
- `G115` `internal/monitor/service/monitor.go:109`: integer overflow conversion `int -> int32`.
- `G115` `internal/monitor/service/monitor.go:89`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/service/billing.go:635`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/service/billing.go:634`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/service/billing.go:633`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/service/billing.go:632`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/service/billing.go:534`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/data/redeem_repo.go:136`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/data/redeem_repo.go:107`: integer overflow conversion `int -> int32`.
- `G115` `internal/billing/data/redeem_repo.go:73`: integer overflow conversion `int -> int32`.
- `G404` `internal/pkg/registry/resolver.go:42`: use of weak random number generator.
- `G402` `internal/pkg/tls/config.go:136`: `InsecureSkipVerify` may be set to true.
- `G402` `internal/pkg/tls/config.go:65`: `InsecureSkipVerify` may be set to true.
- `G704` `internal/admin/server/http.go:2183`: possible SSRF via taint analysis.
- `G704` `internal/admin/server/http.go:2177`: possible SSRF via taint analysis.
- `G703` `internal/admin/server/http.go:853`: possible path traversal via taint analysis.
- `G703` `internal/admin/server/http.go:849`: possible path traversal via taint analysis.

## Medium Priority

- `G118` `internal/pkg/xconfig/source.go:22`: context cancellation function is not called.
- `G705` `internal/relay/server/http.go:1974`: possible XSS via taint analysis.
- `G401` `internal/billing/biz/payment_alipay.go:461`: weak cryptographic primitive.
- `G114` `test/mockupstream/main.go:33`: HTTP server has no configured timeouts.
- `G304` `internal/relay/biz/model_mapping.go:44`: potential file inclusion via variable.
- `G304` `internal/pkg/migrate/runner.go:246`: potential file inclusion via variable.
- `G304` `internal/billing/biz/payment_alipay.go:533`: potential file inclusion via variable.
- `G304` `internal/billing/biz/payment_alipay.go:437`: potential file inclusion via variable.
- `G304` `internal/billing/biz/payment_alipay.go:421`: potential file inclusion via variable.
- `G304` `internal/billing/biz/payment_alipay.go:192`: potential file inclusion via variable.
- `G710` `internal/identity/server/http.go:1531`: possible open redirect via taint analysis.
- `G501` `internal/billing/biz/payment_alipay.go:6`: blocklisted `crypto/md5` import.

## Low Priority

Grouped low-priority gosec findings:

- `G103`: 40 unsafe call audit findings.
- `G104`: 80 unhandled error findings, mostly ignored HTTP response write/encode/close errors.

## Suggested Remediation Order

1. Add checked numeric conversion helpers for API and DB boundary casts, then fix `G115`.
2. Review taint findings around file paths, redirects, SSRF, and XSS with allowlist/path-cleaning tests.
3. Decide whether `InsecureSkipVerify` is test-only/config-gated; document or harden it.
4. Replace non-security randomness only if it affects routing fairness or security-sensitive behavior.
5. Triage low-priority `G103` and `G104` in batches, adding explicit `// #nosec` only after review.
