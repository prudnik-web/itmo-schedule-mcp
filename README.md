# ITMO Schedule MCP

Your ITMO classes in Google Calendar, with automatic updates and useful reminders.
Runs on **macOS, Linux, and Windows** using your own accounts and machine. An
optional read-only MCP server lets AI assistants retrieve schedules too.

- Creates a separate **ITMO** calendar and checks the next 30 days every hour.
- Updates existing events when class details change, without creating duplicates.
- Keeps classes as separate events. Consecutive or overlapping classes with breaks
  of up to 30 minutes share reminders **1 day, 3 hours, and 15 minutes before the
  first class**.
- Connects directly to ITMO by default. No shared server or AI subscription is needed.
- Leaves your primary calendar alone unless you explicitly enable duplicate cleanup.

This is an independent project, not an official ITMO or Google product.

## Get started

You need an ITMO account without an interactive 2FA/CAPTCHA challenge and a
Google account with Calendar. Google requires a one-time Desktop OAuth client;
follow the [Google setup guide](docs/google-setup.md) to download its JSON file.

**ITMO must be reachable from the machine running the sync.** Its API can reject
some networks, so direct mode is not guaranteed from every computer or provider.
If that happens, run on your own host with access or explicitly configure a
[trusted MCP endpoint](docs/calendar-sync.md#optional-trusted-mcp-source).

### Download a release

Download the archive for your machine from
[Releases](https://github.com/prudnik-web/itmo-schedule-mcp/releases), then extract
it completely. Prebuilt downloads become available after the first release is
published; until then, use the source instructions below.

| Machine | Archive suffix |
| --- | --- |
| Mac with Apple silicon | `darwin_arm64.tar.gz` |
| Mac with Intel processor | `darwin_amd64.tar.gz` |
| Linux PC/server, Intel or AMD | `linux_amd64.tar.gz` |
| Linux ARM64 | `linux_arm64.tar.gz` |
| Windows PC, Intel or AMD | `windows_amd64.zip` |
| Windows ARM64 | `windows_arm64.zip` |

Open a terminal in the extracted directory and run:

**macOS / Linux**

```sh
./setup.sh
```

**Windows PowerShell**

```powershell
.\setup.ps1
```

If PowerShell blocks the unsigned script, run the native executable instead:

```powershell
.\itmo-calendar-sync.exe setup
```

Setup asks for your ITMO credentials, checks the schedule, opens Google
authorization, performs the first sync, and installs an hourly task. Your ITMO
password is entered without echo. The scheduler uses a stable copy of the
executable in your private application directory, so you can move the extracted
archive afterward.

Release files include SHA-256 checksums. The binaries are not code-signed; see
[download checks and troubleshooting](docs/troubleshooting.md).

### Build from source

Install [Go 1.26 or newer](https://go.dev/dl/), clone or download this repository,
then run `./setup.sh` on macOS/Linux or `.\setup.ps1` in Windows PowerShell. The
scripts build the executable before starting setup. They do not download or
execute a separate installer.

You can supply the Google client path directly:

```sh
./setup.sh --client /path/to/google-desktop-client.json
```

Use `--no-schedule` to sync manually, or `--no-auth --no-schedule` to configure
ITMO first. All command examples using `itmo-calendar-sync` can be run with
`./itmo-calendar-sync` from a release directory, `./bin/itmo-calendar-sync` from a
source checkout, or the corresponding `.exe` on Windows.

## Everyday commands

```sh
itmo-calendar-sync status
itmo-calendar-sync sync --dry-run
itmo-calendar-sync sync
itmo-calendar-sync uninstall-schedule
itmo-calendar-sync install-schedule
```

[Calendar behavior and scheduling](docs/calendar-sync.md) explains storage paths,
manual cleanup, headless use, and changing accounts. Missing classes are not
assumed cancelled; delete confirmed cancellations yourself.

## Privacy and limitations

Credentials and Google tokens stay in your private application state, outside
this repository. Unix permissions and Windows access controls restrict ordinary
users; an administrator of that machine can still access them. Default mode
sends ITMO credentials to ITMO directly. If you opt into a remote MCP endpoint,
its administrator can inspect the credentials you send.

The computer must be awake and have internet access for a new sync. Google
Calendar delivers reminders for events already synced even while your computer
is off. Desktop scheduling runs in your user session; Linux servers can use
systemd lingering. ITMO's undocumented API and network restrictions can affect
availability. Interactive ITMO 2FA/CAPTCHA accounts are unsupported.

Native builds target all three operating systems. CI is configured to run tests
on macOS, Linux, and Windows once the repository is published; a cross-build
alone is not evidence that an installed task has run successfully on every
target. Check the actual CI results and your local `status` output.

## Optional MCP server

The separate `itmo-schedule-mcp` server exposes one read-only tool:
`get_personal_schedule`. Each caller uses their own ITMO credentials; this server
has no Google Calendar operations.

See [MCP deployment](docs/deployment.md), [client configuration](docs/agent-integration.md),
and [configuration examples](examples/). This server is optional for calendar sync.

## Contributing and license

[Contributions](CONTRIBUTING.md) are welcome. Read [security information](SECURITY.md)
before reporting issues involving account data. Released under [MIT](LICENSE);
[third-party attribution](docs/references.md) is retained.
