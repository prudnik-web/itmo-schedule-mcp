package itmo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const tokenFixture = `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":1800,"refresh_expires_in":7776000}`
const scheduleFixture = `{"code":0,"data":[{"date":"2026-10-26","day_number":1,"week_number":8,"note":null,"lessons":[{"pair_id":900001,"subject":"Synthetic class","subject_id":70001,"time_start":"08:20","time_end":"09:50","teacher_id":200001,"teacher_name":"Test Teacher","room":null,"group":"T3100","zoom_url":"https://example.invalid/class","zoom_password":"not-returned","unexpected_secret":"not-returned"}]},{"date":"2026-10-27","lessons":[]}]}`

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewClient(2 * time.Second)
	client.http = server.Client()
	client.http.CheckRedirect = noRedirect
	client.http.Timeout = 2 * time.Second
	client.issuer = server.URL + "/realm"
	client.redirectURI = "https://my.example.invalid/login/callback"
	client.scheduleURL = server.URL + "/schedule"
	return client
}

func TestLoginRefreshAndSchedule(t *testing.T) {
	var client *Client
	var state, challenge string
	client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realm/protocol/openid-connect/auth":
			q := r.URL.Query()
			if q.Get("client_id") != ClientID || q.Get("redirect_uri") != client.redirectURI || q.Get("scope") != "openid profile" || q.Get("code_challenge_method") != "S256" {
				t.Error("wrong authorization parameters")
			}
			state, challenge = q.Get("state"), q.Get("code_challenge")
			if len(state) < 32 || len(challenge) != 43 {
				t.Error("missing random state or PKCE")
			}
			http.SetCookie(w, &http.Cookie{Name: "login_session", Value: "synthetic-cookie", Path: "/realm"})
			fmt.Fprintf(w, `<script>{"loginAction":%q}</script>`, client.issuer+"/login-actions/authenticate?tab=synthetic&other=1")
		case "/realm/login-actions/authenticate":
			if err := r.ParseForm(); err != nil {
				t.Error("invalid form")
			}
			if r.PostForm.Get("username") != "synthetic-user" || r.PostForm.Get("password") != "synthetic-password" || r.PostForm.Get("rememberMe") != "on" {
				t.Error("wrong credential form")
			}
			if _, err := r.Cookie("login_session"); err != nil {
				t.Error("missing isolated login cookie")
			}
			w.Header().Set("Location", client.redirectURI+"?"+url.Values{"state": {state}, "code": {"synthetic-code"}, "iss": {client.issuer}}.Encode())
			w.WriteHeader(http.StatusFound)
		case "/realm/protocol/openid-connect/token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != ClientID {
				t.Error("wrong token client")
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("code") != "synthetic-code" {
					t.Error("invalid code or verifier")
				}
			case "refresh_token":
				if r.Form.Get("refresh_token") != "synthetic-refresh" || r.Form.Get("scopes") != "openid profile" {
					t.Error("wrong refresh form")
				}
			default:
				t.Error("unexpected grant")
			}
			fmt.Fprint(w, tokenFixture)
		case "/schedule":
			if r.Header.Get("Authorization") != "Bearer synthetic-access" || r.URL.Query().Get("date_start") != "2026-10-26" || r.URL.Query().Get("date_end") != "2026-10-27" {
				t.Error("invalid schedule request")
			}
			if r.Header.Get("Cookie") != "" {
				t.Error("login cookies leaked to schedule")
			}
			fmt.Fprint(w, scheduleFixture)
		default:
			t.Error("unexpected request")
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	tokens, err := client.Login(ctx, Credentials{"synthetic-user", "synthetic-password"})
	if err != nil {
		t.Fatal(err)
	}
	if !tokens.AccessExpires.After(time.Now()) || !tokens.RefreshExpires.After(tokens.AccessExpires) {
		t.Fatal("invalid token lifetimes")
	}
	refreshed, err := client.Refresh(ctx, tokens.Refresh)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Schedule(ctx, refreshed.Access, DateRange{"2026-10-26", "2026-10-27"})
	if err != nil || len(got.Days) != 2 || len(got.Days[0].Lessons) != 1 {
		t.Fatalf("schedule failed: %v", err)
	}
	lesson := got.Days[0].Lessons[0]
	if lesson.Start == nil || *lesson.Start != "2026-10-26T08:20:00+03:00" || lesson.Room != nil || lesson.Building != nil {
		t.Fatal("incorrect normalization")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "not-returned") || strings.Contains(string(encoded), "zoom_password") {
		t.Fatal("unknown sensitive field forwarded")
	}
}

func TestLoginRejectsChallengesAndUntrustedActions(t *testing.T) {
	for _, scenario := range []string{"foreign-action", "wrong-path", "wrong-state", "foreign-callback", "duplicate-state", "wrong-issuer", "otp", "redirect-token"} {
		t.Run(scenario, func(t *testing.T) {
			var client *Client
			var state string
			posts := 0
			client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/realm/protocol/openid-connect/auth":
					state = r.URL.Query().Get("state")
					action := client.issuer + "/login-actions/authenticate"
					if scenario == "foreign-action" {
						action = "https://evil.invalid/realm/login-actions/authenticate"
					}
					if scenario == "wrong-path" {
						action = strings.TrimSuffix(client.issuer, "/realm") + "/untrusted"
					}
					fmt.Fprintf(w, `<form id="kc-form-login" action="%s" method="post"></form>`, action)
				case "/realm/login-actions/authenticate":
					posts++
					if scenario == "otp" {
						fmt.Fprint(w, `<input name="otp">`)
						return
					}
					q := url.Values{"state": {state}, "code": {"synthetic-code"}}
					if scenario == "wrong-state" {
						q.Set("state", "wrong")
					}
					if scenario == "duplicate-state" {
						q.Add("state", state)
					}
					if scenario == "wrong-issuer" {
						q.Set("iss", "https://evil.invalid")
					}
					callback := client.redirectURI
					if scenario == "foreign-callback" {
						callback = "https://evil.invalid/login/callback"
					}
					w.Header().Set("Location", callback+"?"+q.Encode())
					w.WriteHeader(http.StatusFound)
				case "/realm/protocol/openid-connect/token":
					if scenario != "redirect-token" {
						t.Error("token exchange must not happen")
					}
					w.Header().Set("Location", strings.TrimSuffix(client.issuer, "/realm")+"/leak")
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "/leak":
					t.Error("followed credential redirect")
				default:
					t.Error("posted credentials to an untrusted path")
				}
			})
			_, err := client.Login(context.Background(), Credentials{"synthetic-user", "synthetic-password"})
			if err == nil {
				t.Fatal("unsafe login accepted")
			}
			if (scenario == "foreign-action" || scenario == "wrong-path") && posts != 0 {
				t.Fatal("posted credentials to invalid action")
			}
			if strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "http") {
				t.Fatal("error contains upstream details")
			}
		})
	}
}

func TestTokenFailuresAreClassifiedWithoutSecrets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"expired", 400, `{"error":"invalid_grant","error_description":"synthetic-sensitive"}`, AuthenticationRequired},
		{"outage", 503, `synthetic-sensitive`, Unavailable},
		{"limited", 429, `synthetic-sensitive`, RateLimited},
		{"bad-json", 200, `synthetic-sensitive`, ResponseChanged},
		{"incomplete", 200, `{"access_token":"synthetic-sensitive"}`, ResponseChanged},
		{"negative", 200, strings.Replace(tokenFixture, `"expires_in":1800`, `"expires_in":-1`, 1), ResponseChanged},
		{"huge-body", 200, strings.Repeat("x", maxAuthBytes+1), ResponseChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			_, err := client.Refresh(context.Background(), "synthetic-refresh")
			if err != tc.want {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestScheduleRejectsMalformedData(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"code":0}`, `{"code":0,"data":null}`, `{"code":1,"data":[]}`,
		`{"code":0,"data":[{"date":"2026-10-26"}]}`,
		`{"code":0,"data":[{"date":"2026-10-26","lessons":null}]}`,
		`{"code":0,"data":[{"date":"bad","lessons":[]}]}`,
		`{"code":0,"data":[{"date":"2026-10-28","lessons":[]}]}`,
		`{"code":0,"data":[{"date":"2026-10-26","lessons":[]},{"date":"2026-10-26","lessons":[]}]}`,
		strings.Replace(scheduleFixture, "08:20", "25:00", 1),
		strings.Replace(scheduleFixture, "09:50", "08:00", 1),
	} {
		if _, err := parseSchedule([]byte(body), DateRange{"2026-10-26", "2026-10-27"}); err != ResponseChanged {
			t.Fatal("accepted malformed schedule")
		}
	}
	if got, err := parseSchedule([]byte(`{"code":0,"data":[]}`), DateRange{"2026-10-26", "2026-10-27"}); err != nil || got.Days == nil {
		t.Fatal("empty schedule should remain an empty array")
	}
}

func TestScheduleAuthAndNetworkRejection(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{401, "", Unauthorized}, {403, `{"code":90}`, Unauthorized}, {403, `blocked`, Unavailable}, {429, "", RateLimited},
	} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
		_, err := client.Schedule(context.Background(), "synthetic-access", DateRange{"2026-10-26", "2026-10-27"})
		if err != tc.want {
			t.Fatalf("wrong API failure: %v", err)
		}
	}
}

func TestDateDefaultsAndBounds(t *testing.T) {
	now := time.Date(2026, 10, 25, 22, 0, 0, 0, time.UTC)
	got, err := Dates("", "", now)
	if err != nil || got.Start != "2026-10-26" || got.End != "2026-11-01" {
		t.Fatal("defaults must use Moscow date")
	}
	for _, pair := range [][2]string{{"2026-02-29", ""}, {"2026-1-01", ""}, {"2026-10-26", "2026-10-25"}, {"2026-10-01", "2026-11-01"}, {"1999-12-31", ""}} {
		if _, err := Dates(pair[0], pair[1], now); err != InvalidInput {
			t.Fatal("accepted invalid range")
		}
	}
	if _, err := Dates("2026-10-01", "2026-10-31", now); err != nil {
		t.Fatal("31 inclusive days should be valid")
	}
}

func TestSecretFormatting(t *testing.T) {
	for _, value := range []any{Credentials{"synthetic-secret", "synthetic-secret"}, Tokens{Access: "synthetic-secret", Refresh: "synthetic-secret"}} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), "synthetic-secret") {
				t.Fatal("secret formatting leak")
			}
		}
	}
}

func TestPrivateDiagnosticsDoNotExposeUpstreamValues(t *testing.T) {
	var client *Client
	client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth") {
			fmt.Fprintf(w, `<script>{"loginAction":%q}</script>`, client.issuer+"/login-actions/authenticate?secret=synthetic-sensitive")
			return
		}
		fmt.Fprint(w, `<input name="otp"><input value="synthetic-sensitive">`)
	})
	var events []Diagnostic
	client.ObserveDiagnostics(func(event Diagnostic) { events = append(events, event) })
	_, err := client.Login(context.Background(), Credentials{"synthetic-sensitive", "synthetic-sensitive"})
	if err != AuthenticationRequired || len(events) != 3 || events[2].Detail != "interactive_challenge" {
		t.Fatal("private diagnostic did not identify the failed step")
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "synthetic-sensitive") || strings.Contains(string(encoded), "http:") {
		t.Fatal("private diagnostic leaked upstream values")
	}
}
