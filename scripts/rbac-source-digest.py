#!/usr/bin/env python3
"""Hash reviewed build inputs without reading runtime credential files."""
import hashlib
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SUFFIXES = {'.go', '.proto', '.sql', '.ts', '.tsx', '.js', '.json', '.yaml', '.yml', '.sh', '.py'}
NAMES = {'go.mod', 'go.sum', 'Makefile', '.dockerignore', 'Dockerfile', 'tool-versions.env'}


def source_digest():
    paths = subprocess.check_output(
        ['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT)
    digest = hashlib.sha256()
    for name in sorted(set(paths.decode().split('\0')) - {''}):
        path = Path(name)
        if name.startswith('docs/') or (name != 'scripts/tool-versions.env' and any(p.startswith('.env') or p.endswith('.env') for p in path.parts)):
            continue
        if path.suffix not in SUFFIXES and path.name not in NAMES:
            continue
        raw = (ROOT / path).read_bytes()
        # Length framing binds names and contents without ambiguous joins.
        encoded = name.encode()
        digest.update(len(encoded).to_bytes(8, 'big'))
        digest.update(encoded)
        digest.update(len(raw).to_bytes(8, 'big'))
        digest.update(raw)
    return digest.hexdigest()


if __name__ == '__main__':
    print(source_digest())
