package calendarsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
)

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
	host    string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != t.host {
		return nil, errors.New("refusing to forward ITMO credentials to another origin")
	}
	c := r.Clone(r.Context())
	for _, key := range []string{"X-ITMO-Username", "X-ITMO-Password"} {
		c.Header.Set(key, t.headers[key])
	}
	return t.base.RoundTrip(c)
}

func FetchSchedule(ctx context.Context, cfg Config, start, end string) (itmo.Schedule, string, error) {
	var empty itmo.Schedule
	if err := cfg.Validate(); err != nil {
		return empty, "", err
	}
	dates, err := itmo.Dates(start, end, time.Now())
	if err != nil {
		return empty, "", err
	}
	helperCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var data []byte
	if cfg.HeadersFile != "" {
		data, err = os.ReadFile(cfg.HeadersFile)
	} else {
		data, err = exec.CommandContext(helperCtx, cfg.HeadersHelper).Output()
	}
	var headers map[string]string
	if err != nil || json.Unmarshal(data, &headers) != nil || headers["X-ITMO-Username"] == "" || headers["X-ITMO-Password"] == "" {
		return empty, "", errors.New("ITMO credentials unavailable; run setup --reset-credentials or check your configured helper")
	}
	headers["X-ITMO-Username"] = strings.TrimSpace(headers["X-ITMO-Username"])
	identity := cfg.Endpoint
	if cfg.SourceMode() == "direct" {
		identity = itmo.Issuer
	}
	hash := sha256.Sum256([]byte(identity + "\x00" + headers["X-ITMO-Username"]))
	account := hex.EncodeToString(hash[:16])
	if cfg.SourceMode() == "direct" {
		client := itmo.NewClient(20 * time.Second)
		tokens, err := client.Login(ctx, itmo.Credentials{Username: headers["X-ITMO-Username"], Password: headers["X-ITMO-Password"]})
		if err != nil {
			return empty, "", fmtSourceError(err)
		}
		schedule, err := client.Schedule(ctx, tokens.Access, dates)
		if err != nil {
			return empty, "", fmtSourceError(err)
		}
		return schedule, account, nil
	}
	u, _ := url.Parse(cfg.Endpoint)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	hc := &http.Client{Transport: headerTransport{base, headers, u.Host}, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := mcp.NewClient(&mcp.Implementation{Name: "itmo-calendar-sync", Version: "1.0.0"}, &mcp.ClientOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: cfg.Endpoint, HTTPClient: hc}, nil)
	if err != nil {
		return empty, "", errors.New("ITMO MCP connection failed")
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_personal_schedule", Arguments: map[string]string{"date_start": start, "date_end": end}})
	if err != nil || result.IsError {
		return empty, "", errors.New("ITMO schedule request failed; calendar was left unchanged")
	}
	data, err = json.Marshal(result.StructuredContent)
	var schedule itmo.Schedule
	if err != nil || json.Unmarshal(data, &schedule) != nil || schedule.Days == nil || schedule.Timezone != "Europe/Moscow" || schedule.DateStart != start || schedule.DateEnd != end {
		return empty, "", errors.New("invalid ITMO schedule response; calendar was left unchanged")
	}
	return schedule, account, nil
}

func fmtSourceError(err error) error {
	if errors.Is(err, itmo.AuthenticationRequired) {
		return errors.New("ITMO login rejected; check credentials (2FA/CAPTCHA are unsupported)")
	}
	return errors.New("direct ITMO request failed; check connectivity and whether ITMO permits access from this network; see docs/troubleshooting.md")
}
