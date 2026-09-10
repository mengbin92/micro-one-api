#!/usr/bin/env python3
"""Convert the read-only MySQL audit export to deterministic CSV or JSON."""

import argparse
import csv
import json
import sys


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--format", choices=("csv", "json"), required=True)
    args = parser.parse_args()

    rows = list(csv.DictReader(sys.stdin, delimiter="\t"))
    if not rows:
        headers = []
    else:
        headers = list(rows[0].keys())

    if args.format == "csv":
        writer = csv.DictWriter(sys.stdout, fieldnames=headers, lineterminator="\n")
        writer.writeheader()
        writer.writerows(rows)
    else:
        json.dump(rows, sys.stdout, ensure_ascii=False, separators=(",", ":"))
        sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
