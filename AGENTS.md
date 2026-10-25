# Repository guidance

Use Go 1.26+. Build with `make build`; verify with `make check`. Without Make,
run `go test -race ./...`, `go vet ./...`, `python scripts/check_format.py`, and
`go build ./...`. Release packaging is `python3 scripts/release.py --version vX.Y.Z`.

The calendar CLI is separate from the read-only MCP server. Native behavior lives
in `internal/platform`; schedule/auth/API logic lives in `internal/calendarsync`;
ITMO protocol parsing lives in `internal/itmo`. Preserve per-user session isolation.

Keep credentials and personal schedules outside the repository. Tests must use
synthetic values and local servers. Never contact live accounts or install OS
schedules as part of ordinary tests. Keep setup scripts free of remote-code downloads.

Preserve the block reminder rule (same day, gaps <=30 minutes, reminders on first
class only), stable event IDs, explicit Google reminder clearing, and the rule
that missing schedule entries do not establish cancellation. Primary cleanup is
opt-in and must verify the copy and back up the original before deletion.

Document user-facing changes and native validation limits. Do not publish a repo,
push a tag, or publish a GitHub release unless the user requested that action.
