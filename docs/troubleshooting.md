# Troubleshooting

Start with `itmo-calendar-sync --version` and `itmo-calendar-sync status`.
Share the version, OS, and fixed error message when reporting a problem; do not
attach account JSON, token files, schedule previews, or screenshots containing secrets.

## Download and launch

Extract the entire archive matching your OS and CPU. macOS Apple silicon and
Windows/Linux ARM64 need `arm64`; Intel/AMD machines normally need `amd64`.
Prebuilt binaries do not require Go. Source setup requires Go 1.26 or newer.

Check an archive against the release's `SHA256SUMS` before extracting it:

```sh
# macOS
shasum -a 256 itmo-schedule-mcp_vX.Y.Z_darwin_arm64.tar.gz
# Linux
sha256sum itmo-schedule-mcp_vX.Y.Z_linux_amd64.tar.gz
```

```powershell
# Windows PowerShell
Get-FileHash .\itmo-schedule-mcp_vX.Y.Z_windows_amd64.zip -Algorithm SHA256
```

Compare the complete hash with the entry for that filename. These checks detect
corrupted or mismatched downloads; the manifest is not a code-signing certificate.
Binaries and scripts are currently unsigned. If your OS blocks them, inspect the
source and build locally, or use your OS's per-app approval mechanism for the
specific download. On Windows, running `itmo-calendar-sync.exe setup` avoids
PowerShell script-policy restrictions without changing the policy.

## ITMO access

- **Login rejected:** check the account through ITMO.ID. Use
  `setup --reset-credentials` for a changed password. Stop repeated attempts if
  the account is locked or shows an interactive challenge.
- **2FA/CAPTCHA:** the automated login flow cannot complete these challenges.
- **Direct request failed:** check connectivity to `id.itmo.ru` and `my.itmo.ru`.
  ITMO may reject particular networks or hosting providers. A successful TCP/TLS
  connection does not prove the personal schedule endpoint is accessible.
  Run from a network with access or explicitly configure a trusted MCP service.
- **Response changed:** ITMO's API is undocumented. Update the app and report the
  fixed error message with synthetic reproduction details, never a private response.

## Google authorization

- Use a **Desktop app** OAuth client JSON, not a web client or service-account key.
- Enable the Calendar API in the same project that owns that client.
- If the consent screen is in Testing, add your Google account as a test user.
- A Testing refresh token can expire after seven days. Review the publishing
  status and reauthorize after moving to production.
- If access is revoked or refresh fails persistently, run `auth --client PATH`.
- If adding `--migrate-primary`, authorize the additional owned-events permission.
- On a headless server, keep the SSH tunnel open and use the same `--auth-port`
  on the server and in the tunnel. The browser must run on the tunnel's local machine.

See [Google setup](google-setup.md) for detailed steps and provider references.

## Missing reminders or stale events

Check the **ITMO** calendar in Google Calendar and enable app/browser notifications.
Only the first class of a block has reminders. Later classes with gaps up to 30
minutes intentionally have none. Already-passed reminder times cannot be replayed.

Run `sync --dry-run` to compare the current schedule and calendar without applying
changes, then `sync` to apply. A class removed from ITMO is retained until you
confirm cancellation; this prevents accidental deletion after incomplete reads.

If a manually deleted synced event should return, restore it through Google
Calendar's trash where available. The sync respects explicit deletions.

## Scheduled job does not run

- Keep the machine awake, online, and signed in to the configured user account.
- Run `sync` manually to separate connectivity/authentication errors from scheduling.
- On Linux, check that `systemctl --user` works. Servers may need user lingering.
- On Windows, find the app's task in Task Scheduler and inspect its last run result.
- On macOS, use Console/launchd logs and the app's status file.
- After moving to another machine, run setup there; copying source files alone
  does not install a scheduler or authorize its user account.

Use `uninstall-schedule` followed by `install-schedule` to recreate only this
profile's scheduling configuration. Keep private state if you want to preserve
the existing calendar identity and avoid creating a second calendar.
