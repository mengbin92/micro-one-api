#!/usr/bin/env bash
# Use the existing owner-backed harness; never accept its opt-in skip as PASS.
set -euo pipefail
cd "$(dirname "$0")/.."
report="$(mktemp "${TMPDIR:-/tmp}/iam-browser.XXXXXX")"
trap 'mkdir -p web/test-results; cp "$report" web/test-results/iam-go-test.json; rm -f "$report"' EXIT
rm -f web/test-results/iam-playwright.json
IAM_C_PLAYWRIGHT=1 go test -json -count=1 -timeout 10m \
  ./internal/integration -run '^TestIAMCRealBrowserMatrix$' | tee "$report"
python3 scripts/check-iam-browser-result.py "$report" web/test-results/iam-playwright.json
