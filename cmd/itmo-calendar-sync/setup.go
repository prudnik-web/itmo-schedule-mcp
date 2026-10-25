package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/calendarsync"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/platform"
	"golang.org/x/term"
)

type setupOptions struct {
	Client, Endpoint                   string
	NoAuth, NoSchedule, Reset, Migrate bool
	Auth                               calendarsync.AuthOptions
}

func setup(ctx context.Context, dir string, opt setupOptions) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("run setup in your own interactive terminal; passwords are entered without echo")
	}
	input := bufio.NewReader(os.Stdin)
	fmt.Println("ITMO → Google Calendar")
	fmt.Println("Private state:", dir)
	fmt.Println("Credentials are saved for unattended syncing. Your OS account and machine administrators can access them.")
	path := filepath.Join(dir, "config.json")
	cfg, readErr := calendarsync.ReadConfig(dir)
	_, statErr := os.Stat(path)
	if readErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return readErr
	}
	needsCredentials := readErr != nil || opt.Reset
	if readErr != nil {
		cfg = calendarsync.Config{Source: "direct", HeadersFile: filepath.Join(dir, "itmo-headers.json"), LookaheadDays: 30}
		if opt.Endpoint != "" {
			cfg.Source = "mcp"
			cfg.Endpoint = opt.Endpoint
		}
	} else if opt.Endpoint != "" && opt.Endpoint != cfg.Endpoint {
		return errors.New("changing a profile's source can duplicate events; use a separate --dir")
	}
	if opt.Migrate {
		cfg.MigratePrimary = true
		fmt.Println("Primary-calendar cleanup enabled; Google will request access to owned calendars.")
	}
	if needsCredentials {
		cfg.HeadersHelper = ""
		cfg.HeadersFile = filepath.Join(dir, "itmo-headers.json")
		if err := cfg.Validate(); err != nil {
			return err
		}
		if cfg.SourceMode() == "mcp" {
			fmt.Println("ITMO credentials will be sent to:", cfg.Endpoint)
			fmt.Println("This server's administrator can inspect them. Use only a server you trust.")
		} else {
			fmt.Println("ITMO credentials will be sent directly to ITMO.")
		}
		username, err := promptLine(input, "ITMO username: ")
		if err != nil {
			return err
		}
		fmt.Print("ITMO password (hidden): ")
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return errors.New("could not read password")
		}
		if username == "" || len(password) == 0 {
			return errors.New("username and password are required")
		}
		headers := map[string]string{"X-ITMO-Username": username, "X-ITMO-Password": string(password)}
		if err := calendarsync.WriteJSON(cfg.HeadersFile, headers); err != nil {
			return err
		}
	}
	if err := calendarsync.WriteJSON(path, cfg); err != nil {
		return err
	}
	fmt.Println("Checking your ITMO connection…")
	start := time.Now().In(itmo.Moscow).Format(time.DateOnly)
	_, account, err := calendarsync.FetchSchedule(ctx, cfg, start, start)
	if err != nil {
		return err
	}
	if cfg.AccountKey != "" && cfg.AccountKey != account {
		return errors.New("these credentials belong to a different account; use a new --dir")
	}
	fmt.Println("ITMO connection verified.")
	if opt.NoAuth {
		fmt.Println("Configuration saved. Run setup again to authorize Google and enable syncing.")
		return nil
	}
	_, tokenErr := os.Stat(filepath.Join(dir, "google-token.json"))
	if tokenErr != nil || opt.Client != "" || opt.Migrate {
		client := opt.Client
		if client == "" {
			candidate := filepath.Join(dir, "google-client.json")
			if _, err := os.Stat(candidate); err == nil {
				client = candidate
			} else {
				fmt.Println("Create a Google Desktop OAuth client first; see docs/google-setup.md.")
				fmt.Println("Guide: https://github.com/prudnik-web/itmo-schedule-mcp/blob/main/docs/google-setup.md")
				client, err = promptLine(input, "Path to downloaded Google client JSON: ")
				if err != nil {
					return err
				}
			}
		}
		if err := calendarsync.Authorize(ctx, dir, expandUserPath(client), opt.Auth); err != nil {
			return err
		}
	}
	fmt.Println("Syncing upcoming classes to the ITMO calendar…")
	if err := syncOnce(ctx, dir, cfg, false, false); err != nil {
		return err
	}
	if !opt.NoSchedule {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := platform.InstallSchedule(ctx, exe, dir); err != nil {
			return fmt.Errorf("first sync succeeded, but scheduling failed: %w; run install-schedule after fixing it", err)
		}
		fmt.Println("Hourly syncing enabled. The computer must be running; see the scheduling guide for login/sleep behavior.")
	}
	fmt.Println("Setup complete. First class in each block: reminders 1 day, 3 hours, and 15 minutes before it starts.")
	return nil
}

func promptLine(r *bufio.Reader, label string) (string, error) {
	fmt.Print(label)
	s, err := r.ReadString('\n')
	if err != nil {
		return "", errors.New("input ended; run setup again to continue")
	}
	return strings.TrimSpace(s), nil
}

func expandUserPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
