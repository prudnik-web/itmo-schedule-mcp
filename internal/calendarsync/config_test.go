package calendarsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSourceConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "headers.json")
	for _, tt := range []struct {
		name  string
		cfg   Config
		mode  string
		valid bool
	}{
		{"direct default", Config{HeadersFile: file, LookaheadDays: 30}, "direct", true},
		{"explicit direct", Config{Source: "direct", HeadersFile: file, LookaheadDays: 30}, "direct", true},
		{"legacy MCP", Config{Endpoint: "https://itmo.example.com/mcp", HeadersFile: file, LookaheadDays: 30}, "mcp", true},
		{"MCP", Config{Source: "mcp", Endpoint: "https://itmo.example.com/mcp", HeadersFile: file, LookaheadDays: 30}, "mcp", true},
		{"ambiguous mode", Config{Source: "direct", Endpoint: "https://itmo.example.com/mcp", HeadersFile: file, LookaheadDays: 30}, "", false},
		{"insecure MCP", Config{Source: "mcp", Endpoint: "http://itmo.example.com/mcp", HeadersFile: file, LookaheadDays: 30}, "", false},
		{"credential in URL", Config{Source: "mcp", Endpoint: "https://user:pass@itmo.example.com/mcp", HeadersFile: file, LookaheadDays: 30}, "", false},
		{"missing credential source", Config{Source: "direct", LookaheadDays: 30}, "", false},
		{"two credential sources", Config{Source: "direct", HeadersFile: file, HeadersHelper: file, LookaheadDays: 30}, "", false},
		{"relative file", Config{Source: "direct", HeadersFile: "headers.json", LookaheadDays: 30}, "", false},
		{"unknown source", Config{Source: "other", HeadersFile: file, LookaheadDays: 30}, "", false},
		{"excessive date range", Config{Source: "direct", HeadersFile: file, LookaheadDays: 32}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err == nil) != tt.valid {
				t.Fatalf("validation %v, want valid=%v", err, tt.valid)
			}
			if tt.valid && tt.cfg.SourceMode() != tt.mode {
				t.Fatal("wrong source mode")
			}
		})
	}
}

func TestPrivateConfigRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	cfg := Config{Source: "direct", HeadersFile: filepath.Join(dir, "itmo-headers.json"), LookaheadDays: 30}
	if err := WriteJSON(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfig(dir)
	if err != nil || got != cfg {
		t.Fatalf("config roundtrip: %+v %v", got, err)
	}
	if got.MigratePrimary {
		t.Fatal("primary calendar cleanup must be opt-in")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("configuration must be owner-only")
		}
	}
}

func TestInvalidSourceCannotExecuteCredentialHelper(t *testing.T) {
	_, _, err := FetchSchedule(context.Background(), Config{Source: "invalid", HeadersHelper: filepath.Join(t.TempDir(), "does-not-exist"), LookaheadDays: 30}, "2026-10-25", "2026-10-26")
	if err == nil || !strings.Contains(err.Error(), "source must") {
		t.Fatalf("source must be rejected before helper execution: %v", err)
	}
}

func TestDefaultGoogleScopesDoNotAllowPrimaryWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.json")
	data := map[string]any{"installed": map[string]string{"client_id": "synthetic-client.apps.googleusercontent.com", "client_secret": "synthetic-client-value"}}
	if err := WriteJSON(path, data); err != nil {
		t.Fatal(err)
	}
	cfg, err := oauthConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range cfg.Scopes {
		if scope == "https://www.googleapis.com/auth/calendar.events.owned" || scope == "https://www.googleapis.com/auth/calendar" {
			t.Fatal("broad scope enabled by default")
		}
	}
	encoded, _ := json.Marshal(cfg.Scopes)
	if !strings.Contains(string(encoded), "calendar.app.created") {
		t.Fatal("app-created calendar scope missing")
	}
}
