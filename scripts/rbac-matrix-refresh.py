#!/usr/bin/env python3
"""Refresh SOURCE digests in docs/design/rbac/entry-matrix.csv.

CI (make rbac-contract-check) fails on any source drift between the RBAC
entry matrix and the Go packages it pins. Run this after touching a file
that appears in the matrix (app/*/internal/server, app/identity/internal/{biz,data},
internal/server, ...) so the failure surfaces locally instead of on push.

Usage:
  python3 scripts/rbac-matrix-refresh.py            # rewrite stale digests
  python3 scripts/rbac-matrix-refresh.py --check    # exit 1 if any stale (CI-style)
"""
import csv
import subprocess
import sys

MATRIX = "docs/design/rbac/entry-matrix.csv"


def inventory_digests() -> dict:
    out = subprocess.run(
        ["go", "run", "./cmd/rbac-contract-check", "-dump"],
        capture_output=True, text=True, check=True,
    )
    digests = {}
    for row in csv.reader(out.stdout.splitlines()):
        if row and row[0] == "SOURCE":
            digests[(row[1], row[2])] = row[5]
    return digests


def main() -> int:
    check_only = "--check" in sys.argv[1:]
    digests = inventory_digests()
    with open(MATRIX, newline="") as f:
        rows = list(csv.reader(f))
    stale = [
        (i, digests[(row[1], row[2])])
        for i, row in enumerate(rows)
        if i > 0 and row and row[0] == "SOURCE"
        and (row[1], row[2]) in digests and row[5] != digests[(row[1], row[2])]
    ]
    if not stale:
        print("rbac matrix: all SOURCE digests current")
        return 0
    for i, row in enumerate(rows):
        if any(i == idx for idx, _ in stale):
            row[5] = next(d for idx, d in stale if idx == i)
    if check_only:
        for idx, _ in stale:
            print(f"stale SOURCE digest at row {idx + 1}: {rows[idx][2]}")
        print("run: python3 scripts/rbac-matrix-refresh.py")
        return 1
    with open(MATRIX, "w", newline="") as f:
        csv.writer(f).writerows(rows)
    for idx, _ in stale:
        print(f"refreshed row {idx + 1}: {rows[idx][2]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
