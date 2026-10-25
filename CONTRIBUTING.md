# Contributing

Use Go 1.26 or newer. Python 3 is needed only for repository formatting checks and
release packaging; end users of prebuilt binaries do not need it.

```sh
make check build
```

On Windows, or without Make:

```sh
go test -race ./...
go vet ./...
python scripts/check_format.py
go build ./...
```

Tests use synthetic accounts and local HTTP servers. Keep live ITMO/Google calls
out of ordinary tests and CI. Do not install real schedules as part of tests.
Prefer targeted regression tests for changed account isolation, matching,
reminders, permissions, and scheduler behavior.

Before submitting a pull request:

- Format Go files with `gofmt` and run the relevant checks.
- Explain the concrete problem and changed behavior.
- Update setup/user documentation when commands or permissions change.
- Keep credentials, user schedules, machine-specific paths, and account exports out
  of source, tests, logs, screenshots, and issue reports.
- Preserve the source references and required license notices.

## Layout

- `cmd/itmo-calendar-sync`: guided setup and local calendar CLI.
- `internal/calendarsync`: schedule mapping, Google OAuth/API, and synchronization.
- `internal/platform`: native permissions, locks, browser opening, and scheduling.
- `internal/itmo`: university authentication and schedule parsing.
- `cmd/itmo-schedule-mcp`, `internal/mcpserver`, `internal/session`: optional
  multi-user, read-only MCP service with isolated in-memory sessions.
- `scripts/release.py`: allowlisted build archives and checksums.

## Releases

Run `python3 scripts/release.py --version v0.1.0` to build all six target archives
under `dist/v0.1.0/`. `--targets darwin/arm64` limits a local packaging check.
The script never uploads or publishes anything. Archives include executables,
setup entrypoints, documentation, and license notices; they do not copy the
checkout or private state wholesale.
The builder also collects the Go runtime license and dependency notices from
the modules used by the distributed commands into `THIRD_PARTY_NOTICES.txt`.

After the repository is published, a pushed `v*` tag runs native CI on macOS,
Linux, and Windows, cross-builds six archives, and creates a **draft** GitHub
release. Review the checksums, extract/run the correct archive on each target,
and inspect the notes before publishing the draft. Unsigned binaries may prompt
for OS approval; do not describe them as signed or notarized.

CI workflow execution and native installation tests require their actual hosts.
A successful cross-build on one machine is not evidence of runtime success on
another. Include the limits of your validation in release notes.

## License

Contributions are provided under the project's MIT license. Third-party notices
in `docs/references.md` must remain with source and binary distributions.
