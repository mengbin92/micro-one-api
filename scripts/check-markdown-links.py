#!/usr/bin/env python3
"""Check that local links in README.md and docs/**/*.md resolve to files.

Links must stay inside the repository. A relative target that escapes the
project root (for example `../../../new-api/service/group.go`) is rejected
even when the sibling checkout happens to exist on the developer machine:
CI checks out this repository alone, so such a link can only ever pass
locally. Reference material outside the repository belongs in a code span,
not in a link. See docs/deployment.md §1.1.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parent.parent
MARKDOWN_FILES = [ROOT / "README.md", *sorted((ROOT / "docs").rglob("*.md"))]
INLINE_LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
REFERENCE_LINK_RE = re.compile(r"^\s*\[[^\]]+\]:\s*(\S+)")
EXTERNAL_PREFIXES = ("http://", "https://", "mailto:", "tel:", "data:")


def normalize_target(raw: str) -> str:
    target = raw.strip()
    if target.startswith("<") and ">" in target:
        target = target[1 : target.index(">")]
    else:
        target = target.split(maxsplit=1)[0]
    return unquote(target)


def is_within(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
    except ValueError:
        return False
    return True


def main() -> int:
    missing: list[tuple[Path, int, str]] = []
    outside: list[tuple[Path, int, str]] = []

    for markdown in MARKDOWN_FILES:
        in_fence = False
        fence_marker = ""
        for line_number, line in enumerate(markdown.read_text(encoding="utf-8").splitlines(), 1):
            stripped = line.lstrip()
            if stripped.startswith(("```", "~~~")):
                marker = stripped[:3]
                if not in_fence:
                    in_fence = True
                    fence_marker = marker
                elif marker == fence_marker:
                    in_fence = False
                continue
            if in_fence:
                continue

            targets = INLINE_LINK_RE.findall(line)
            reference = REFERENCE_LINK_RE.match(line)
            if reference:
                targets.append(reference.group(1))

            for raw_target in targets:
                target = normalize_target(raw_target)
                if not target or target.startswith("#") or target.startswith(EXTERNAL_PREFIXES):
                    continue

                path_part = target.split("#", 1)[0].split("?", 1)[0]
                if not path_part:
                    continue
                destination = (markdown.parent / path_part).resolve()
                if not is_within(destination, ROOT):
                    outside.append((markdown.relative_to(ROOT), line_number, target))
                elif not destination.exists():
                    missing.append((markdown.relative_to(ROOT), line_number, target))

    failed = False

    if missing:
        failed = True
        print("Broken local Markdown links:", file=sys.stderr)
        for markdown, line_number, target in missing:
            print(f"  {markdown}:{line_number}: {target}", file=sys.stderr)

    if outside:
        failed = True
        print(
            "Markdown links escaping the repository (use a code span instead):",
            file=sys.stderr,
        )
        for markdown, line_number, target in outside:
            print(f"  {markdown}:{line_number}: {target}", file=sys.stderr)

    if failed:
        return 1

    print(f"Markdown local links: OK ({len(MARKDOWN_FILES)} files checked)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
