// smoke verifies the remote service without printing credentials or schedules.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
	"golang.org/x/term"
)

type credentialTransport struct {
	base        http.RoundTripper
	credentials itmo.Credentials
}

func (t credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("X-ITMO-Username", t.credentials.Username)
	clone.Header.Set("X-ITMO-Password", t.credentials.Password)
	return t.base.RoundTrip(clone)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run() error {
	endpoint := flag.String("url", "https://itmo.example.com/mcp", "HTTPS MCP endpoint")
	discover := flag.Bool("discover", false, "Check protocol and tool discovery only; no login or schedule call")
	schemas := flag.String("write-schemas", "", "Write discovered tool schemas to this directory")
	sshTarget := flag.String("ssh-target", "", "Also verify upstream login and refresh over SSH, e.g. root@your-vps")
	sshKey := flag.String("ssh-key", "", "Existing private-key path for optional SSH verification")
	waitOnFailure := flag.Bool("wait-on-failure", false, "Keep credentials in memory and wait for manual Enter to retry after a failure")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/mcp" {
		return errors.New("use an HTTPS URL ending in /mcp")
	}
	credentials := itmo.Credentials{Username: "schema-discovery", Password: "not-an-itmo-login"}
	if !*discover {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("run this command in your own terminal; credentials must be entered interactively")
		}
		credentials.Username, err = prompt("ITMO username (hidden): ")
		if err != nil {
			return err
		}
		credentials.Password, err = prompt("ITMO password (hidden): ")
		if err != nil {
			return err
		}
		credentials.Username = strings.TrimSpace(credentials.Username)
		if credentials.Username == "" || credentials.Password == "" {
			return itmo.MissingCredentials
		}
	}
	for {
		err := check(credentials, *endpoint, *discover, *schemas, *sshTarget, *sshKey)
		if err == nil || !*waitOnFailure {
			return err
		}
		fmt.Fprintln(os.Stderr, err.Error())
		fmt.Fprint(os.Stderr, "Press Enter to retry after the fix, or q to stop: ")
		answer, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		if readErr != nil || strings.TrimSpace(answer) != "" {
			return err
		}
	}
}

func check(credentials itmo.Credentials, endpoint string, discover bool, schemas, sshTarget, sshKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	httpClient := &http.Client{Transport: credentialTransport{base: base, credentials: credentials}, Timeout: 55 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := mcp.NewClient(&mcp.Implementation{Name: "itmo-private-smoke", Version: "0.1.0"}, &mcp.ClientOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}, nil)
	if err != nil {
		return errors.New("MCP connection failed; check HTTPS, endpoint and credential headers")
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "get_personal_schedule" || list.Tools[0].OutputSchema == nil {
		return errors.New("MCP tool discovery failed")
	}
	fmt.Println("MCP connection and get_personal_schedule discovery: OK")
	if schemas != "" {
		if err := os.MkdirAll(schemas, 0755); err != nil {
			return errors.New("cannot create schema directory")
		}
		for name, schema := range map[string]any{"input.schema.json": list.Tools[0].InputSchema, "output.schema.json": list.Tools[0].OutputSchema} {
			data, err := json.MarshalIndent(schema, "", "  ")
			if err != nil {
				return errors.New("cannot serialize schema")
			}
			if os.WriteFile(filepath.Join(schemas, name), append(data, '\n'), 0644) != nil {
				return errors.New("cannot write schema")
			}
		}
	}
	if discover {
		return nil
	}
	if sshTarget != "" {
		if strings.HasPrefix(sshTarget, "-") || strings.ContainsAny(sshTarget, " \t\r\n") {
			return errors.New("invalid SSH target")
		}
		args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10"}
		if sshKey != "" {
			args = append(args, "-i", sshKey)
		}
		args = append(args, sshTarget, "/opt/itmo-schedule-mcp/itmo-schedule-mcp", "verify-itmo")
		command := exec.CommandContext(ctx, "ssh", args...)
		encoded, _ := json.Marshal(credentials)
		command.Stdin = bytes.NewReader(encoded)
		output, err := command.Output()
		var check struct {
			Login, Refresh, Schedule string
			Error                    itmo.Error
			Diagnostics              []itmo.Diagnostic
		}
		if json.Unmarshal(output, &check) != nil {
			return errors.New("SSH upstream verification failed; check SSH access")
		}
		for _, diagnostic := range check.Diagnostics {
			// Only fixed identifiers/status codes are accepted for console output.
			if !safeLabel(diagnostic.Step) || !safeLabel(diagnostic.Detail) || diagnostic.Status < 0 || diagnostic.Status > 599 {
				return errors.New("invalid diagnostic response")
			}
			fmt.Printf("ITMO diagnostic: %s status=%d %s\n", diagnostic.Step, diagnostic.Status, diagnostic.Detail)
		}
		if err != nil || check.Error != "" {
			return itmo.PublicError(check.Error)
		}
		if check.Login != "ok" || check.Refresh != "ok" || check.Schedule != "ok" {
			return errors.New("upstream verification returned an invalid result")
		}
		fmt.Println("VPS direct login, token refresh and schedule retrieval: OK")
	}
	dates, _ := itmo.Dates("", "", time.Now())
	for range 2 {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_personal_schedule", Arguments: map[string]string{"date_start": dates.Start, "date_end": dates.End}})
		if err != nil {
			return errors.New("MCP schedule request failed")
		}
		if result.IsError {
			for _, content := range result.Content {
				if text, ok := content.(*mcp.TextContent); ok {
					switch itmo.Error(text.Text) {
					case itmo.AuthenticationRequired, itmo.Unavailable, itmo.ResponseChanged, itmo.RateLimited, itmo.InvalidInput, itmo.MissingCredentials:
						return itmo.Error(text.Text)
					}
				}
			}
			return errors.New("MCP returned a tool error")
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return errors.New("invalid structured result")
		}
		var schedule itmo.Schedule
		if json.Unmarshal(data, &schedule) != nil || schedule.Days == nil || schedule.Timezone != "Europe/Moscow" {
			return errors.New("invalid schedule structure")
		}
		lessons := 0
		for _, day := range schedule.Days {
			lessons += len(day.Lessons)
		}
		fmt.Printf("Schedule: OK (%d days, %d lessons; personal contents hidden)\n", len(schedule.Days), lessons)
	}

	return nil
}

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", errors.New("could not read credential prompt")
	}
	return string(value), nil
}

func safeLabel(value string) bool {
	if len(value) > 64 {
		return false
	}
	for _, r := range value {
		if r != '_' && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}
