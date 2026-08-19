#!/usr/bin/env bash
# Enforce the AGENTS.md "Commits & security" rule: non-trivial commits must
# carry a body describing root cause + impact, not just a subject line.
#
# Usage:
#   scripts/check-commit-bodies.sh <range>   # e.g. origin/develop..HEAD
#
# Exempt (title-only is acceptable):
#   - merge commits (have >1 parent)
#   - trivial subjects: version bumps, changelog/release-notes-only commits,
#     and "Merge ..." subjects
# Everything else (feat/fix/refactor/perf/test/docs-with-substance/...) must
# have a non-empty body.
set -euo pipefail

range="${1:?usage: $0 <git-range> (e.g. origin/develop..HEAD)}"

trivial_re='^(chore\(release\): bump|bump version|docs\(changelog\)|Merge )'

violations=0
while IFS= read -r commit; do
  # Merge commits are exempt.
  if [ "$(git rev-list --parents -n1 "$commit" | wc -w | tr -d ' ')" -gt 2 ]; then
    continue
  fi
  subject="$(git log -1 --format='%s' "$commit")"
  if printf '%s' "$subject" | grep -qE "$trivial_re"; then
    continue
  fi
  # Non-whitespace body content required.
  if ! git log -1 --format='%b' "$commit" | grep -q '[^[:space:]]'; then
    printf '  MISSING BODY  %s  %s\n' "$(git log -1 --format='%h' "$commit")" "$subject"
    violations=$((violations + 1))
  fi
done < <(git rev-list --no-merges "$range")

if [ "$violations" -gt 0 ]; then
  echo
  echo "error: $violations commit(s) in range '$range' have a subject but no body." >&2
  echo "AGENTS.md requires non-trivial commits to describe root cause + impact." >&2
  exit 1
fi
echo "commit bodies OK: all non-trivial commits in '$range' have a body"
