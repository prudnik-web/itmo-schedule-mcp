package calendarsync

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/platform"
	"golang.org/x/oauth2"
)

var scopes = []string{
	"https://www.googleapis.com/auth/calendar.app.created",
	"https://www.googleapis.com/auth/calendar.calendarlist.readonly",
}

func oauthConfig(path string) (*oauth2.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("Google Desktop OAuth client JSON is missing; see docs/calendar-sync.md")
	}
	var file struct {
		Installed struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
	}
	if json.Unmarshal(data, &file) != nil || file.Installed.ClientID == "" || file.Installed.ClientSecret == "" {
		return nil, errors.New("expected a Google OAuth Desktop app client JSON file")
	}
	return &oauth2.Config{ClientID: file.Installed.ClientID, ClientSecret: file.Installed.ClientSecret, Scopes: scopes,
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}}, nil
}

// Authorize uses the supported Desktop loopback flow, with state and PKCE.
type AuthOptions struct {
	NoBrowser bool
	Port      int
}

func Authorize(ctx context.Context, dir, clientPath string, options AuthOptions) error {
	if options.Port < 0 || options.Port > 65535 {
		return errors.New("auth-port must be between 0 and 65535")
	}
	app, err := ReadConfig(dir)
	if err != nil {
		return err
	}
	cfg, err := oauthConfig(clientPath)
	if err != nil {
		return err
	}
	if app.MigratePrimary {
		cfg.Scopes = append(append([]string{}, cfg.Scopes...), "https://www.googleapis.com/auth/calendar.events.owned")
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", options.Port))
	if err != nil {
		return errors.New("cannot listen on localhost for Google authorization")
	}
	cfg.RedirectURL = "http://" + ln.Addr().String() + "/callback"
	state, verifier := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	type response struct {
		code string
		err  error
	}
	result := make(chan response, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization state", http.StatusBadRequest)
			return
		}
		res := response{code: r.URL.Query().Get("code")}
		if res.code == "" || r.URL.Query().Get("error") != "" {
			res.err = errors.New("Google authorization was not granted")
		}
		select {
		case result <- res:
		default:
		}
		fmt.Fprintln(w, "Authorization received. You can close this tab and return to your terminal.")
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer srv.Close()
	go srv.Serve(ln)
	link := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier))
	fmt.Println("Authorize your Google account to enable calendar syncing.")
	if options.NoBrowser {
		fmt.Println("Open this authorization URL (use a localhost SSH tunnel for a remote machine):", link)
	} else if err := platform.OpenBrowser(ctx, link); err != nil {
		fmt.Println("Open this authorization URL:", link)
	}
	var res response
	select {
	case res = <-result:
	case <-ctx.Done():
		return errors.New("Google authorization timed out; run auth again")
	}
	if res.err != nil {
		return res.err
	}
	token, err := cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return errors.New("Google token exchange failed; check the OAuth client and try auth again")
	}
	if token.RefreshToken == "" {
		return errors.New("Google did not return an offline refresh token; revoke the app grant and authorize again")
	}
	if scope, ok := token.Extra("scope").(string); ok && scope != "" {
		granted := map[string]bool{}
		for _, s := range strings.Fields(scope) {
			granted[s] = true
		}
		for _, s := range cfg.Scopes {
			if !granted[s] {
				return errors.New("Google authorization is missing a requested scope; run auth again and grant all requested permissions")
			}
		}
	}
	clientData, err := os.ReadFile(clientPath)
	if err != nil {
		return err
	}
	var stored any
	if err := json.Unmarshal(clientData, &stored); err != nil {
		return err
	}
	if err := WriteJSON(filepath.Join(dir, "google-client.json"), stored); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(dir, "google-token.json"), token)
}

type savedTokenSource struct {
	mu     sync.Mutex
	source oauth2.TokenSource
	path   string
	last   string
}

func (s *savedTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.source.Token()
	if err != nil {
		return nil, errors.New("Google authorization expired or could not refresh; check connectivity or run auth again")
	}
	if t.AccessToken != s.last {
		if err := WriteJSON(s.path, t); err != nil {
			return nil, errors.New("cannot persist refreshed Google token")
		}
		s.last = t.AccessToken
	}
	return t, nil
}

func GoogleHTTPClient(ctx context.Context, dir string) (*http.Client, error) {
	cfg, err := oauthConfig(filepath.Join(dir, "google-client.json"))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "google-token.json")
	data, err := os.ReadFile(path)
	var token oauth2.Token
	if err != nil || json.Unmarshal(data, &token) != nil || token.RefreshToken == "" {
		return nil, errors.New("Google authorization missing; run auth --client /path/to/client.json")
	}
	client := oauth2.NewClient(ctx, &savedTokenSource{source: cfg.TokenSource(ctx, &token), path: path, last: token.AccessToken})
	client.Timeout = 45 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}
