package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
)

type providerFunc func(context.Context, itmo.Credentials, string, itmo.DateRange) (itmo.Schedule, error)

func (f providerFunc) Get(ctx context.Context, c itmo.Credentials, ip string, d itmo.DateRange) (itmo.Schedule, error) {
	return f(ctx, c, ip, d)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSDKRoundTripIsolationAndJSONCompatibility(t *testing.T) {
	var mu sync.Mutex
	var users []string
	var logs bytes.Buffer
	provider := providerFunc(func(ctx context.Context, c itmo.Credentials, ip string, d itmo.DateRange) (itmo.Schedule, error) {
		mu.Lock()
		users = append(users, c.Username)
		mu.Unlock()
		if c.Password != "synthetic-password" {
			t.Error("credential context lost")
		}
		if d.Start != "2026-10-26" || d.End != "2026-10-27" {
			t.Error("wrong tool dates")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("work has no deadline")
		}
		start := "08:20"
		return itmo.Schedule{DateStart: d.Start, DateEnd: d.End, Timezone: "Europe/Moscow", Days: []itmo.Day{{Date: d.Start, Lessons: []itmo.Lesson{{TimeStart: &start}}}, {Date: d.End, Lessons: []itmo.Lesson{}}}}, nil
	})
	ts := httptest.NewUnstartedServer(nil)
	origin := "https://" + ts.Listener.Addr().String()
	handler, err := New(provider, Config{PublicOrigin: origin, LocalAddress: "127.0.0.1:3700", Concurrency: 4, RequestsPerMinute: 120, RequestTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = handler
	ts.StartTLS()
	defer ts.Close()
	username := "synthetic-alice"
	base := ts.Client().Transport
	httpClient := ts.Client()
	httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		clone.Header.Set("X-ITMO-Username", username)
		clone.Header.Set("X-ITMO-Password", "synthetic-password")
		clone.Header.Set("Mcp-Session-Id", "caller-chosen-session")
		return base.RoundTrip(clone)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "get_personal_schedule" || list.Tools[0].OutputSchema == nil || !list.Tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("discovery failed: %v", err)
	}
	for _, user := range []string{"synthetic-alice", "synthetic-bob"} {
		username = user
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_personal_schedule", Arguments: Arguments{DateStart: "2026-10-26", DateEnd: "2026-10-27"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("tool failed: %+v", result.Content)
		}
		if result.StructuredContent == nil || len(result.Content) != 1 {
			t.Fatal("missing typed or text content")
		}
		var decoded itmo.Schedule
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || json.Unmarshal([]byte(text.Text), &decoded) != nil || len(decoded.Days) != 2 {
			t.Fatal("invalid JSON text fallback")
		}
		if decoded.Days[0].Lessons[0].Room != nil {
			t.Fatal("absent room was invented")
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "synthetic-password") {
			t.Fatal("password in response")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(users, ",") != "synthetic-alice,synthetic-bob" {
		t.Fatal("HTTP requests shared identity")
	}
	if strings.Contains(logs.String(), "synthetic-") {
		t.Fatal("identity or credential logged")
	}
}

func testHandler(t *testing.T, provider ScheduleProvider, rpm int) http.Handler {
	t.Helper()
	h, err := New(provider, Config{PublicOrigin: "https://itmo.example.invalid", LocalAddress: "127.0.0.1:3700", Concurrency: 1, RequestsPerMinute: rpm, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func testRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://itmo.example.invalid/mcp", strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("X-ITMO-Username", "synthetic-user")
	r.Header.Set("X-ITMO-Password", "synthetic-password")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2025-06-18")
	return r
}

func TestHTTPGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"missing-password", func(r *http.Request) { r.Header.Del("X-ITMO-Password") }, 401},
		{"duplicate-username", func(r *http.Request) { r.Header.Add("X-ITMO-Username", "other") }, 401},
		{"oversize-password", func(r *http.Request) { r.Header.Set("X-ITMO-Password", strings.Repeat("x", 4097)) }, 401},
		{"host", func(r *http.Request) { r.Host = "evil.invalid" }, 403},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.invalid") }, 403},
		{"null-origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"plaintext", func(r *http.Request) { r.TLS = nil }, 403},
		{"spoofed-proxy", func(r *http.Request) {
			r.TLS = nil
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-For", "192.0.2.2")
		}, 403},
		{"bad-proxy", func(r *http.Request) {
			r.TLS = nil
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-For", "192.0.2.1, 192.0.2.2")
		}, 403},
		{"get", func(r *http.Request) { r.Method = "GET" }, 405},
		{"huge", func(r *http.Request) { r.ContentLength = MaxRequestBytes + 1 }, 413},
		{"batch", func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`))
			r.ContentLength = -1
		}, 400},
		{"chunked-huge", func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", MaxRequestBytes+1)))
			r.ContentLength = -1
		}, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t, providerFunc(func(context.Context, itmo.Credentials, string, itmo.DateRange) (itmo.Schedule, error) {
				t.Error("guarded request reached upstream")
				return itmo.Schedule{}, nil
			}), 100)
			r := testRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
			tc.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "synthetic-password") {
				t.Fatal("HTTP error leaked password")
			}
		})
	}
}

func TestRateLimitingIgnoresUntrustedForwardedHeader(t *testing.T) {
	h := testHandler(t, nil, 1)
	for i, forwarded := range []string{"192.0.2.2", "192.0.2.3"} {
		r := testRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 0 && w.Code != 200 {
			t.Fatalf("discovery failed: %s", w.Body.String())
		}
		if i == 1 && w.Code != 429 {
			t.Fatal("untrusted XFF bypassed rate limit")
		}
	}
}

func TestToolErrorRedactionAndInvalidDates(t *testing.T) {
	provider := providerFunc(func(context.Context, itmo.Credentials, string, itmo.DateRange) (itmo.Schedule, error) {
		return itmo.Schedule{}, fmtError("synthetic-sensitive upstream error")
	})
	h := testHandler(t, provider, 100)
	for _, tc := range []struct{ args, category string }{
		{`{"date_start":"2026-02-30"}`, "invalid_input"},
		{`{"date_start":"2026-10-26"}`, "itmo_unavailable"},
	} {
		r := testRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_personal_schedule","arguments":` + tc.args + `}}`)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), tc.category) || strings.Contains(w.Body.String(), "synthetic-sensitive") {
			t.Fatalf("unexpected tool error: %s", w.Body.String())
		}
	}
}

type fmtError string

func (e fmtError) Error() string { return string(e) }

func TestHealthNeedsNoCredentials(t *testing.T) {
	h := testHandler(t, nil, 100)
	r := httptest.NewRequest("GET", "http://127.0.0.1:3700/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatal("health check failed")
	}
}
