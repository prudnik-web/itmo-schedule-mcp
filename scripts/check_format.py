#!/usr/bin/env python3
"""Check Go formatting consistently on macOS, Linux, and Windows."""
import pathlib
import subprocess
import sys

root = pathlib.Path(__file__).resolve().parents[1]
files = sorted(str(path.relative_to(root)) for folder in ('cmd', 'internal')
               for path in (root / folder).rglob('*.go'))
result = subprocess.run(['gofmt', '-l', *files], cwd=root, capture_output=True, text=True)
if result.returncode:
    sys.stderr.write(result.stderr)
    sys.exit(result.returncode)
if result.stdout.strip():
    sys.stderr.write('Run gofmt on:\n' + result.stdout)
    sys.exit(1)
