// itmo-calendar-sync runs independently of an AI client or hosted MCP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/calendarsync"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/platform"
)

var version = "dev"

const usage = `ITMO Calendar Sync

  itmo-calendar-sync setup                 Guided setup and hourly scheduling
  itmo-calendar-sync auth --client FILE    Authorize/re-authorize Google
  itmo-calendar-sync sync [--dry-run]      Synchronize or preview change counts
  itmo-calendar-sync preview               Read ITMO only; prints personal event JSON
  itmo-calendar-sync status                Show the last recorded sync result
  itmo-calendar-sync install-schedule      Enable the native hourly scheduler
  itmo-calendar-sync uninstall-schedule    Disable scheduler; keep events and data

All commands accept --dir PATH for a separate profile. Use COMMAND --help for options.
Start with setup; no hosted server or AI client is required.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] == "--help" || os.Args[1] == "-h" {
		fmt.Print(usage)
		return nil
	}
	if os.Args[1] == "--version" || os.Args[1] == "version" {
		fmt.Println("itmo-calendar-sync", version)
		return nil
	}
	command := os.Args[1]
	switch command {
	case "setup", "auth", "sync", "preview", "status", "install-schedule", "uninstall-schedule":
	default:
		return errors.New("unknown command; use --help")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := flags.String("dir", calendarsync.DefaultDir(), "Private application state directory")
	var client, endpoint string
	var dry, scheduled, noAuth, noSchedule, reset, migrate, noBrowser bool
	var authPort int
	if command == "setup" || command == "auth" {
		flags.StringVar(&client, "client", "", "Google Desktop OAuth client JSON")
		flags.BoolVar(&noBrowser, "no-browser", false, "Print Google authorization URL instead of launching a browser")
		flags.IntVar(&authPort, "auth-port", 0, "Loopback port (0 chooses a free port; use a fixed port for an SSH tunnel)")
	}
	if command == "setup" {
		flags.StringVar(&endpoint, "endpoint", "", "Optional trusted HTTPS MCP endpoint instead of direct ITMO access")
		flags.BoolVar(&migrate, "migrate-primary", false, "Back up and remove confidently matched primary-calendar duplicates")
		flags.BoolVar(&noAuth, "no-auth", false, "Configure/check ITMO only; skip Google and scheduling")
		flags.BoolVar(&noSchedule, "no-schedule", false, "Complete setup without installing automatic scheduling")
		flags.BoolVar(&reset, "reset-credentials", false, "Prompt for replacement ITMO credentials")
	}
	if command == "sync" {
		flags.BoolVar(&dry, "dry-run", false, "Read calendars and report counts without changing events")
		flags.BoolVar(&scheduled, "scheduled", false, "Wait quietly when initial Google authorization is missing")
	}
	if err := flags.Parse(os.Args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	if authPort < 0 || authPort > 65535 {
		return errors.New("auth-port must be between 0 and 65535")
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	*dir = abs
	if command == "status" {
		return printStatus(*dir)
	}
	if err := platform.EnsurePrivateDir(*dir); err != nil {
		return err
	}
	unlock, err := platform.Lock(filepath.Join(*dir, "sync.lock"))
	if errors.Is(err, platform.ErrLocked) {
		fmt.Println("Another operation is already running for this profile.")
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	auth := calendarsync.AuthOptions{NoBrowser: noBrowser, Port: authPort}
	switch command {
	case "setup":
		return setup(ctx, *dir, setupOptions{Client: client, Endpoint: endpoint, NoAuth: noAuth, NoSchedule: noSchedule, Reset: reset, Migrate: migrate, Auth: auth})
	case "auth":
		if client == "" {
			client = filepath.Join(*dir, "google-client.json")
		} else {
			client = expandUserPath(client)
		}
		if err := calendarsync.Authorize(ctx, *dir, client, auth); err != nil {
			return err
		}
		fmt.Println("Google authorization saved. Run sync --dry-run to preview or sync to apply.")
		return nil
	case "install-schedule":
		if _, err := calendarsync.ReadConfig(*dir); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := platform.InstallSchedule(ctx, exe, *dir); err != nil {
			return err
		}
		fmt.Println("Hourly scheduling enabled for this profile.")
		return nil
	case "uninstall-schedule":
		if err := platform.RemoveSchedule(ctx, *dir); err != nil {
			return err
		}
		fmt.Println("Hourly scheduling disabled. Calendar events and private state are retained.")
		return nil
	}
	cfg, err := calendarsync.ReadConfig(*dir)
	if err != nil {
		return err
	}
	if scheduled {
		if _, err := os.Stat(filepath.Join(*dir, "google-token.json")); errors.Is(err, os.ErrNotExist) {
			fmt.Println("waiting_for_google_authorization")
			return calendarsync.WriteJSON(filepath.Join(*dir, "status.json"), map[string]any{"state": "waiting_for_google_authorization", "checked_at": time.Now().UTC().Format(time.RFC3339)})
		}
	}
	err = syncOnce(ctx, *dir, cfg, dry, command == "preview")
	if err != nil && command == "sync" && !dry {
		_ = calendarsync.WriteJSON(filepath.Join(*dir, "status.json"), map[string]any{"state": "error", "checked_at": time.Now().UTC().Format(time.RFC3339), "error": err.Error()})
	}
	return err
}

func syncOnce(ctx context.Context, dir string, cfg calendarsync.Config, dry, preview bool) error {
	now := time.Now()
	start := now.In(itmo.Moscow).Format(time.DateOnly)
	end := now.In(itmo.Moscow).AddDate(0, 0, cfg.LookaheadDays-1).Format(time.DateOnly)
	schedule, account, err := calendarsync.FetchSchedule(ctx, cfg, start, end)
	if err != nil {
		return err
	}
	if cfg.AccountKey != "" && cfg.AccountKey != account {
		return errors.New("ITMO account or source changed; use setup --dir PATH with a separate profile")
	}
	cfg.AccountKey = account
	events, err := calendarsync.BuildEvents(schedule, account, now)
	if err != nil {
		return err
	}
	if preview {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"date_start": start, "date_end": end, "events": events})
	}
	hc, err := calendarsync.GoogleHTTPClient(ctx, dir)
	if err != nil {
		return err
	}
	google := &calendarsync.Google{HTTP: hc}
	if !dry {
		if _, err := google.EnsureCalendar(ctx, &cfg); err != nil {
			return err
		}
		if err := calendarsync.WriteJSON(filepath.Join(dir, "config.json"), cfg); err != nil {
			return err
		}
	}
	stats, err := calendarsync.Sync(ctx, google, cfg, dir, events, now, dry)
	if err != nil {
		return err
	}
	if !dry {
		if err := calendarsync.WriteJSON(filepath.Join(dir, "status.json"), map[string]any{"state": "ok", "last_success": now.UTC().Format(time.RFC3339), "stats": stats}); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(stats)
}

func printStatus(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("No sync result recorded yet. Run setup or sync.")
		return nil
	}
	if err != nil {
		return err
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return errors.New("invalid status file")
	}
	return json.NewEncoder(os.Stdout).Encode(value)
}
