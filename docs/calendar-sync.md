# Calendar sync

## What gets synced

The app reads today through 29 days ahead using `Europe/Moscow` and creates
individual events in a separate Google calendar named **ITMO**. It includes
lectures, practicals, consultations, exams, and overlapping classes. Classroom
links, teacher, group, notes, and location are included when the source supplies them.

Stable account/lesson IDs prevent duplicates across repeated runs. Changes to
class times and details update the same event. Past/ongoing classes are not
rewritten. A manually deleted synced event stays deleted.

A class disappearing from ITMO is retained: the source does not reliably identify
cancellations, so an empty/incomplete response must not delete your calendar.
Remove confirmed cancellations manually. Errors stop the run; later scheduled
runs retry. Updates use Google version tags and concurrent local runs are locked.

## Reminders

Classes on one day separated by **30 minutes or less** form a block, even when
the subjects differ. Overlapping classes are part of the same block. Only its
first event has reminders **1 day, 3 hours, and 15 minutes** before its start.
A larger gap or a new day starts another block.

For example, **18:50–20:20 + 20:30–22:00** produces three reminders for 18:50,
with no repeated reminders for 20:30. Earlier lessons remain grouping context
when a sync runs during a class or break. Each class remains a separate event.

Enable Google Calendar notifications on your devices. A reminder time already
in the past cannot trigger retroactively. The app sets Google popup/app reminders,
not email reminders.

## Automatic scheduling

`setup` installs an hourly task after the first authorized sync. The executable
is copied into the private application directory; tasks do not run from your
Downloads folder or source checkout. Schedules belong to the current user and
are keyed to the state directory, allowing separate profiles.

| Operating system | Scheduler | Session behavior |
| --- | --- | --- |
| macOS | Per-user launchd agent | Runs while logged in; calendar scheduling catches up after sleep |
| Linux | systemd user timer | Requires a user systemd manager; enable lingering for a server that must run after logout |
| Windows | Task Scheduler | Runs as the current user while logged in; no Windows password is collected |

The machine needs power and network access for syncing. Existing Google events
and reminders work independently of the sync machine. The setup does not wake
a powered-off computer or promise a run while a user session is unavailable.

```sh
itmo-calendar-sync status
itmo-calendar-sync install-schedule
itmo-calendar-sync uninstall-schedule
```

Removing the schedule leaves credentials, backups, and Google events intact.
To stop syncing permanently, remove the schedule, revoke the app's access in
your Google account, and remove the private state directory when you no longer
need its backups. Find the default location in the storage table below, or use
the same custom `--dir` you supplied during setup.

The scheduler name is `itmo-calendar-sync-` followed by a profile-specific hash.
Its name and configuration paths are recorded in private `schedule.json`.
macOS writes task output to `sync.log` and `sync-error.log` in the state directory;
Linux uses the user service journal, and Windows exposes last-run results in
Task Scheduler. Scheduling uses native OS facilities; it has not been live-tested
on every supported platform merely because the release archives compile.

On a Linux server, an administrator can enable your user manager after logout:

```sh
sudo loginctl enable-linger YOUR_USERNAME
```

This affects your systemd user manager, not just this app. A machine without
systemd can run `itmo-calendar-sync sync --scheduled` from its existing scheduler;
pass an absolute `--dir` and use a stable executable path.

## Storage and accounts

| OS | Default private state |
| --- | --- |
| macOS | `~/Library/Application Support/itmo-calendar-sync/` |
| Linux | `$XDG_CONFIG_HOME/itmo-calendar-sync/`, or `~/.config/itmo-calendar-sync/` |
| Windows | `%LOCALAPPDATA%\itmo-calendar-sync\` |

Configuration points to private ITMO credentials and stores the chosen calendar
ID. Google client metadata, refresh tokens, status, and duplicate backups live
there too. Unix state uses owner-only permissions; Windows state uses a
restricted access-control list. These files are not protected from administrators
or software running as your user.

Use `--dir /absolute/private/path` consistently for a separate profile. The app
refuses to reuse an existing calendar configuration after the ITMO account
fingerprint changes. Use a new profile for another account. Run
`setup --reset-credentials` to replace a changed ITMO password for the same account.

## Primary-calendar cleanup is optional

Pass `--migrate-primary` during setup only if you want to remove existing ITMO
class duplicates from your primary calendar. It is disabled by default and needs
the extra Google permission described in [Google setup](google-setup.md).

Cleanup considers only upcoming events within the sync window. Both start and end
must match a source class, along with a full subject title or exact classroom URL.
Recurring entries, events with guests, ambiguous matches, and historical events
are excluded. The destination is read back and verified before the complete
original event is saved under `primary-backups/` and removed from the primary
calendar. Set `migrate_primary` to `false` in private `config.json` to stop cleanup.

## Headless server

You can authorize on a server through an SSH tunnel without copying credentials
through chat or exposing a callback listener publicly:

1. On your desktop, open a terminal and keep this tunnel running:

   ```sh
   ssh -L 8765:127.0.0.1:8765 YOUR_USER@YOUR_SERVER
   ```

2. In that SSH session, run setup with a Desktop OAuth JSON file already placed
   in a private server directory:

   ```sh
   itmo-calendar-sync setup --client /private/google-desktop-client.json --no-browser --auth-port 8765
   ```

3. Open the printed Google authorization URL in your desktop browser. The
   callback to localhost travels through the SSH tunnel. Return to the terminal
   for the first-sync result.

The listener binds only to server loopback. No public callback endpoint or
firewall opening is needed. Reauthorization uses the same flags on `auth`.
Server administrators can read secrets stored or processed there.

## Optional trusted MCP source

Direct ITMO access is the default. If ITMO is inaccessible from your machine's
network, run the app on a machine with access or use an MCP service you operate
or explicitly trust:

```sh
itmo-calendar-sync setup --dir /absolute/private/mcp-profile --endpoint https://itmo.example.com/mcp
```

Replace the example domain. This mode sends your ITMO username/password to the
MCP host over HTTPS, so its administrator can inspect them. It does not send your
Google token to the MCP. Do not change source mode inside an established profile;
use a new profile and account/calendar configuration.
