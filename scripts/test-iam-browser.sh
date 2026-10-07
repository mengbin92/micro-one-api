#!/usr/bin/env bash
# Use the existing owner-backed harness; never accept its opt-in skip as PASS.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p web/iam-test-results
IAM_BROWSER_RESULT_DIR="$(mktemp -d "$PWD/web/iam-test-results/run.XXXXXX")"
export IAM_BROWSER_RESULT_DIR
echo "IAM acceptance artifacts: $IAM_BROWSER_RESULT_DIR"
# Go's JSON and browser artifacts are unique to this run, so neither another
# IAM run nor the ordinary Playwright smoke can erase or supply these results.
IAM_C_PLAYWRIGHT=1 go test -json -count=1 -timeout 10m \
  ./internal/integration -run '^TestIAMCRealBrowserMatrix$' | tee "$IAM_BROWSER_RESULT_DIR/go-test.json"
python3 scripts/check-iam-browser-result.py "$IAM_BROWSER_RESULT_DIR/go-test.json" "$IAM_BROWSER_RESULT_DIR/playwright.json"
