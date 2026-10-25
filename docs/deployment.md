# Deployment and operations

The service needs a machine whose network can access both `id.itmo.ru` and
`my.itmo.ru`. An ITMO HTTP 403 with code 90 means a missing/rejected token; a generic
403 may mean a network restriction. Check network access from your chosen host before deploying.

## Example Linux layout

- Domain: `itmo.example.com`; MCP `/mcp`; health `/healthz`.
- Binary: `/opt/itmo-schedule-mcp/itmo-schedule-mcp`.
- Unit: `/etc/systemd/system/itmo-schedule-mcp.service`.
- Service listener: `127.0.0.1:3700`, not publicly bound.
- Caddy route snippet: `/etc/caddy/itmo-schedule-mcp.routes`.
- Caddy runtime logging snippet: `/etc/caddy/itmo-schedule-mcp.logging`.
- Optional server tuning: `/etc/itmo-schedule-mcp.env`; **no user credentials**.
- No database, persistent token cache or background schedule fetcher.

## Install

Replace `itmo.example.com` with your own domain in the environment file and
Caddy site configuration. The sample domain is not a hosted service.

Build on a trusted machine with Go 1.26+:

```sh
make check linux
```

Copy `bin/itmo-schedule-mcp-linux-amd64`, the systemd unit and the two Caddy
snippets to a private staging directory on the Linux amd64 VPS over SSH/SCP.
Use an existing SSH identity; never put a private key in the repository.

Before modifications, inspect the existing Caddy routes and service ports, verify
DNS resolves to this VPS, and back up the current Caddyfile and any previous
binary/unit/snippets. Install the binary atomically rather than truncating the
running executable:

```sh
install -d -m 755 /opt/itmo-schedule-mcp
install -m 755 itmo-schedule-mcp-linux-amd64 /opt/itmo-schedule-mcp/itmo-schedule-mcp.new
mv /opt/itmo-schedule-mcp/itmo-schedule-mcp.new /opt/itmo-schedule-mcp/itmo-schedule-mcp
install -m 644 itmo-schedule-mcp.service /etc/systemd/system/itmo-schedule-mcp.service
install -m 644 Caddyfile.routes /etc/caddy/itmo-schedule-mcp.routes
install -m 644 Caddyfile.logging /etc/caddy/itmo-schedule-mcp.logging
```

Merge into `/etc/caddy/Caddyfile`:

```caddyfile
{
    import /etc/caddy/itmo-schedule-mcp.logging
}

itmo.example.com {
    import /etc/caddy/itmo-schedule-mcp.routes
    # Preserve the site's existing root, encode, header and file_server directives.
}
```

There may be only one global block. If there is already a `log default` block,
merge the two filter rules into it. Preserve unrelated sites, custom loggers and
global options. Runtime error logs are distinct from access logs; `log_skip`
alone does not prevent custom headers appearing on proxy failures. Caddy uses
canonical header names `X-Itmo-Username` and `X-Itmo-Password` in the filter.
Do not enable debug/trace logging or add other request log sinks without checking
the same redaction rules. See [Caddy logging](https://caddyserver.com/docs/caddyfile/directives/log)
and [runtime log options](https://caddyserver.com/docs/caddyfile/options#log).

Validate and activate:

```sh
systemd-analyze verify /etc/systemd/system/itmo-schedule-mcp.service
caddy validate --config /etc/caddy/Caddyfile
systemctl daemon-reload
systemctl enable --now itmo-schedule-mcp
systemctl reload caddy
curl --fail https://itmo.example.com/healthz
```

DNS/TLS are Caddy's normal automatic HTTPS deployment. Keep port 3700 closed to
external traffic. The application trusts forwarded IP/protocol headers only from
loopback; the Caddy route overwrites those fields from the real connection.

The shipped unit uses a dynamic unprivileged user, read-only filesystem, no core
dumps, 96 MiB memory ceiling and four concurrent HTTP requests as example deployment limits. Go's 48 MiB soft heap target is not a total RSS limit. If legitimate schedules
exhaust memory, inspect pressure and adjust both concurrency and memory limits;
do not remove response bounds or dump request contents to diagnose it.

## Configuration

| Variable | Standalone default | Allowed/meaning |
| --- | --- | --- |
| `ITMO_LISTEN` | `127.0.0.1:3700` | Literal loopback IP and port only |
| `ITMO_PUBLIC_ORIGIN` | `https://itmo.example.com` | HTTPS origin, without path/trailing slash/query |
| `ITMO_CACHE_SIZE` | `256` | 1–10000 credential-pair sessions |
| `ITMO_CACHE_IDLE` | `24h` | 1s–168h; idle sweep every minute |
| `ITMO_CONCURRENCY` | `32` | 1–256; deployment unit overrides to 4 |
| `ITMO_REQUESTS_PER_MINUTE` | `120` | 1–10000 per client IP |
| `ITMO_UPSTREAM_TIMEOUT` | `12s` | 1s–20s per ITMO HTTP request |

Fixed bounds: 31 inclusive dates, 4 MiB schedule body, 1 MiB authentication body,
200 lessons per returned day, 16 KiB MCP body and HTTP header limit, 45-second tool
deadline, 50-second HTTP write timeout. Rate-limit maps hold at most 10000 keys;
new keys are rejected if live counters fill them. Login limits are five per user
and twenty per IP per ten-minute fixed window. Fixed windows can permit a burst
around a boundary. Restart clears all in-memory state and counters.
Legacy JSON-RPC batches are rejected so a single HTTP request cannot bypass
concurrency and response-memory limits by invoking many tools at once.

## Verification

First run discovery from the client machine:

```sh
go run ./cmd/smoke --url https://itmo.example.com/mcp --discover
```

This uses placeholder headers and performs initialization and `tools/list` only.
Discovery verifies MCP/TLS, **not** ITMO authentication or schedule availability.

For the final live test, the owner runs the following in a private local terminal,
replacing the SSH target and key path for their own deployment:

```sh
go run ./cmd/smoke --url https://itmo.example.com/mcp --ssh-target root@YOUR_VPS --ssh-key /path/to/existing/ssh-key
```

Both credentials are read without echo. After protocol discovery, the client
sends the credentials in memory over SSH stdin to the server binary's private
`verify-itmo` command. That command verifies a fresh ITMO login, immediate refresh,
and schedule retrieval. The client then performs two remote MCP schedule calls.
Nothing secret goes in command arguments, shell history, files or test output.
Output contains only fixed diagnostic step labels, statuses and lesson/day counts.
No production auth check runs as part of ordinary `go test`.

For interactive debugging, add `--wait-on-failure`. The client keeps credentials
only in its own process memory and waits for Enter before another attempt; it
never retries automatically. Type `q` to exit. If the entered credentials were
wrong, exit and restart to enter corrected values. Private diagnostics never
include upstream bodies/URLs, even on authentication failure, and are not enabled
in the public server process.

Do not paste credentials or schedule output into chat. Report only success or the
stable error category. Repeated wrong-password tests can lock the university
account; fixture tests already cover failures without real account traffic.

## Status, upgrades and recovery

```sh
systemctl is-active itmo-schedule-mcp caddy
systemctl status itmo-schedule-mcp --no-pager
journalctl -u itmo-schedule-mcp --since '30 minutes ago' --no-pager
ss -ltn 'sport = :3700'
```

Application logs contain fixed outcomes and durations only. Do not enable body,
header or SDK debug logging. Check Caddy errors after confirming its filters are
installed. Never use `curl -v` with real credentials.

For an upgrade, rerun checks, save the old binary, install a new binary via rename,
and `systemctl restart itmo-schedule-mcp`. Repeat health/discovery and a private
schedule call. Other users automatically sign in on their next call.

- **Authentication required:** validate credentials directly with ITMO.ID. An
  interactive challenge is unsupported. Stop repeated attempts.
- **Unavailable:** check VPS connectivity/DNS and ITMO status. A transient refresh
  failure preserves tokens; retry later. Do not replace it with a login loop.
- **Response changed:** inspect public reference changes and update synthetic
  fixtures/validation. Do not log a real response to diagnose it.
- **Rate limited:** wait for the relevant window; shared NATs share IP quotas.
- **TLS problem:** check DNS and Caddy certificate renewal. Never fall back to
  public HTTP with credentials.
- **Rollback:** restore backed-up Caddyfile/snippets, validate and reload Caddy;
  restore the previous binary and restart the unit. On a first-install rollback,
  disable/stop only `itmo-schedule-mcp`. Preserve all other sites/services.

Passwords are never retained in cache entries, but bearer/refresh tokens are
usable credentials in memory. Ordinary Go memory cannot promise immediate zeroing
of strings. Revoking university sessions remains the user's control for a lost
client config. Changing the configured password forces a new session for that
credential pair; previously cached tokens depend on ITMO revocation/expiry and
the cache's idle/eviction bounds.
