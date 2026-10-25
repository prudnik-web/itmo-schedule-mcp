#!/bin/sh
# Uses a bundled binary or builds from source; no remote installer script.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -x "$ROOT/itmo-calendar-sync" ]; then
    exec "$ROOT/itmo-calendar-sync" setup "$@"
fi
if [ ! -f "$ROOT/go.mod" ]; then
    printf '%s\n' 'The release executable is missing. Extract the entire archive for your operating system.' >&2
    exit 1
fi
if ! command -v go >/dev/null 2>&1; then
    printf '%s\n' 'Install Go 1.26 or newer to build from source, or download a prebuilt release archive.' >&2
    exit 1
fi
cd "$ROOT"
mkdir -p bin
go build -trimpath -o bin/itmo-calendar-sync ./cmd/itmo-calendar-sync
exec "$ROOT/bin/itmo-calendar-sync" setup "$@"
