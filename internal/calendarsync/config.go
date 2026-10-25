// Package calendarsync implements the local, unattended ITMO calendar client.
package calendarsync

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/platform"
)

type Config struct {
	Source         string `json:"source,omitempty"`
	Endpoint       string `json:"endpoint"`
	HeadersHelper  string `json:"headers_helper"`
	HeadersFile    string `json:"headers_file,omitempty"`
	CalendarID     string `json:"calendar_id,omitempty"`
	AccountKey     string `json:"account_key,omitempty"`
	LookaheadDays  int    `json:"lookahead_days"`
	MigratePrimary bool   `json:"migrate_primary"`
}

func DefaultDir() string {
	return platform.DefaultDir()
}

func (cfg Config) SourceMode() string {
	if cfg.Source != "" {
		return cfg.Source
	}
	if cfg.Endpoint != "" {
		return "mcp"
	}
	return "direct"
}

func (cfg Config) Validate() error {
	if cfg.LookaheadDays < 1 || cfg.LookaheadDays > 31 || (cfg.HeadersHelper == "") == (cfg.HeadersFile == "") || (cfg.HeadersHelper != "" && !filepath.IsAbs(cfg.HeadersHelper)) || (cfg.HeadersFile != "" && !filepath.IsAbs(cfg.HeadersFile)) {
		return errors.New("invalid configuration: choose one absolute credential file/helper path and 1–31 lookahead days")
	}
	switch cfg.SourceMode() {
	case "direct":
		if cfg.Endpoint != "" {
			return errors.New("direct mode must not specify an MCP endpoint")
		}
	case "mcp":
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/mcp" {
			return errors.New("MCP mode requires an HTTPS endpoint ending in /mcp")
		}
	default:
		return errors.New("source must be direct or mcp")
	}
	return nil
}

func ReadConfig(dir string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return cfg, errors.New("configuration missing; run itmo-calendar-sync setup first")
	}
	if json.Unmarshal(data, &cfg) != nil {
		return cfg, errors.New("invalid configuration")
	}
	return cfg, cfg.Validate()
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := platform.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
