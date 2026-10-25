package itmo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

const (
	Issuer           = "https://id.itmo.ru/auth/realms/itmo"
	ClientID         = "student-personal-cabinet"
	RedirectURI      = "https://my.itmo.ru/login/callback"
	ScheduleURL      = "https://my.itmo.ru/api/schedule/schedule/personal"
	maxAuthBytes     = 1 << 20
	maxScheduleBytes = 4 << 20
)

type Credentials struct{ Username, Password string }

func (Credentials) String() string   { return "[credentials redacted]" }
func (Credentials) GoString() string { return "[credentials redacted]" }

type Tokens struct {
	Access, Refresh               string
	AccessExpires, RefreshExpires time.Time
}

func (Tokens) String() string   { return "[tokens redacted]" }
func (Tokens) GoString() string { return "[tokens redacted]" }

// Client has no shared cookie jar. Each password login gets an isolated jar.
// Endpoint overrides are deliberately not exposed as server configuration.
type Client struct {
	observe                                    func(Diagnostic)
	http                                       *http.Client
	issuer, clientID, redirectURI, scheduleURL string
	now                                        func() time.Time
}

func NewClient(timeout time.Duration) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	transport.MaxConnsPerHost = 32
	transport.ResponseHeaderTimeout = timeout
	return &Client{
		http:   &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: noRedirect},
		issuer: Issuer, clientID: ClientID, redirectURI: RedirectURI, scheduleURL: ScheduleURL, now: time.Now,
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *Client) Login(ctx context.Context, credentials Credentials) (Tokens, error) {
	if strings.TrimSpace(credentials.Username) == "" || credentials.Password == "" {
		return Tokens{}, MissingCredentials
	}
	verifier := randomString()
	state := randomString()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"protocol": {"oauth2"}, "response_type": {"code"}, "client_id": {c.clientID},
		"redirect_uri": {c.redirectURI}, "scope": {"openid profile"}, "state": {state},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
	}
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return Tokens{}, Unavailable
	}
	local := *c.http
	local.Jar = jar
	status, _, page, err := request(ctx, &local, http.MethodGet, c.issuer+"/protocol/openid-connect/auth?"+query.Encode(), nil, "", maxAuthBytes)
	c.diagnostic("login_page", status, "")
	if err != nil {
		return Tokens{}, err
	}
	if status != http.StatusOK {
		return Tokens{}, statusError(status)
	}
	action := loginAction(page)
	actionURL, err := url.Parse(action)
	issuerURL, _ := url.Parse(c.issuer)
	if err != nil || action == "" {
		return Tokens{}, ResponseChanged
	}
	actionURL = issuerURL.ResolveReference(actionURL)
	if !sameOrigin(actionURL, issuerURL) || actionURL.User != nil || actionURL.Fragment != "" ||
		!strings.HasPrefix(actionURL.Path, issuerURL.Path+"/login-actions/") {
		return Tokens{}, ResponseChanged
	}
	status, headers, answer, err := request(ctx, &local, http.MethodPost, actionURL.String(), url.Values{
		"username": {credentials.Username}, "password": {credentials.Password}, "rememberMe": {"on"},
	}, "", maxAuthBytes)
	c.diagnostic("password_submit", status, "")
	if err != nil {
		return Tokens{}, err
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return Tokens{}, statusError(status)
	}
	if status != http.StatusFound && status != http.StatusSeeOther {
		c.diagnostic("login_response", status, pageCategory(answer))
		return Tokens{}, AuthenticationRequired
	}
	code, err := c.callbackCode(headers.Get("Location"), state)
	if err != nil {
		return Tokens{}, err
	}
	return c.tokens(ctx, url.Values{
		"code": {code}, "client_id": {c.clientID}, "redirect_uri": {c.redirectURI},
		"response_type": {"code"}, "grant_type": {"authorization_code"}, "code_verifier": {verifier},
	})
}

func (c *Client) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	if refresh == "" {
		return Tokens{}, AuthenticationRequired
	}
	return c.tokens(ctx, url.Values{
		"refresh_token": {refresh}, "scopes": {"openid profile"}, "client_id": {c.clientID}, "grant_type": {"refresh_token"},
	})
}

func (c *Client) tokens(ctx context.Context, form url.Values) (Tokens, error) {
	status, _, body, err := request(ctx, c.http, http.MethodPost, c.issuer+"/protocol/openid-connect/token", form, "", maxAuthBytes)
	step := "code_exchange"
	if form.Get("grant_type") == "refresh_token" {
		step = "token_refresh"
	}
	c.diagnostic(step, status, "")
	if err != nil {
		return Tokens{}, err
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return Tokens{}, statusError(status)
	}
	var wire struct {
		Access         string `json:"access_token"`
		Refresh        string `json:"refresh_token"`
		Expires        *int64 `json:"expires_in"`
		RefreshExpires *int64 `json:"refresh_expires_in"`
		Error          string `json:"error"`
	}
	if json.Unmarshal(body, &wire) != nil {
		return Tokens{}, ResponseChanged
	}
	if wire.Error == "invalid_grant" || wire.Error == "access_denied" || status == http.StatusUnauthorized {
		return Tokens{}, AuthenticationRequired
	}
	if status != http.StatusOK || wire.Error != "" {
		return Tokens{}, Unavailable
	}
	if strings.TrimSpace(wire.Access) == "" || strings.TrimSpace(wire.Refresh) == "" ||
		wire.Expires == nil || *wire.Expires <= 0 || *wire.Expires > 366*24*3600 ||
		wire.RefreshExpires == nil || *wire.RefreshExpires < 0 || *wire.RefreshExpires > 10*366*24*3600 {
		return Tokens{}, ResponseChanged
	}
	now := c.now()
	result := Tokens{Access: wire.Access, Refresh: wire.Refresh, AccessExpires: now.Add(time.Duration(*wire.Expires) * time.Second)}
	// Keycloak uses zero for a refresh token with no advertised expiry.
	if *wire.RefreshExpires > 0 {
		result.RefreshExpires = now.Add(time.Duration(*wire.RefreshExpires) * time.Second)
	}
	return result, nil
}

func (c *Client) callbackCode(location, state string) (string, error) {
	u, err := url.Parse(location)
	expected, _ := url.Parse(c.redirectURI)
	if err != nil || !sameOrigin(u, expected) || u.User != nil || u.Fragment != "" ||
		u.EscapedPath() != expected.EscapedPath() {
		c.diagnostic("callback", 0, "origin_or_path_mismatch")
		return "", AuthenticationRequired
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		c.diagnostic("callback", 0, "invalid_query")
		return "", AuthenticationRequired
	}
	for _, values := range q {
		if len(values) != 1 {
			c.diagnostic("callback", 0, "duplicate_query_key")
			return "", AuthenticationRequired
		}
	}
	if state == "" || q.Get("state") != state || q.Has("error") || (q.Has("iss") && q.Get("iss") != c.issuer) {
		detail := "state_mismatch"
		if q.Has("error") {
			detail = "oauth_error"
		} else if q.Has("iss") && q.Get("iss") != c.issuer {
			detail = "issuer_mismatch"
		}
		c.diagnostic("callback", 0, detail)
		return "", AuthenticationRequired
	}
	code := q.Get("code")
	if strings.TrimSpace(code) == "" || len(code) > 4096 {
		c.diagnostic("callback", 0, "missing_or_invalid_code")
		return "", AuthenticationRequired
	}
	c.diagnostic("callback", 0, "accepted")
	return code, nil
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

var actionPattern = regexp.MustCompile(`"loginAction"\s*:\s*("(?:[^"\\]|\\.)*")`)

func loginAction(page []byte) string {
	if match := actionPattern.FindSubmatch(page); len(match) == 2 {
		var value string
		if json.Unmarshal(match[1], &value) == nil {
			return html.UnescapeString(value)
		}
	}
	root, err := xhtml.Parse(strings.NewReader(string(page)))
	if err != nil {
		return ""
	}
	var walk func(*xhtml.Node) string
	walk = func(n *xhtml.Node) string {
		if n.Type == xhtml.ElementNode && n.Data == "form" {
			var id, action string
			for _, attr := range n.Attr {
				if attr.Key == "id" {
					id = attr.Val
				}
				if attr.Key == "action" {
					action = attr.Val
				}
			}
			if id == "kc-form-login" {
				return action
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if result := walk(child); result != "" {
				return result
			}
		}
		return ""
	}
	return walk(root)
}

func randomString() string {
	var buf [32]byte
	_, _ = rand.Read(buf[:]) // crypto/rand terminates the process on entropy failure.
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

func statusError(status int) error {
	if status == http.StatusTooManyRequests {
		return RateLimited
	}
	return Unavailable
}

func request(ctx context.Context, client *http.Client, method, endpoint string, form url.Values, bearer string, maxBytes int64) (int, http.Header, []byte, error) {
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, nil, Unavailable
	}
	req.Header.Set("User-Agent", "itmo-schedule-mcp/0.1")
	req.Header.Set("Accept", "application/json, text/html;q=0.9")
	req.Header.Set("Accept-Language", "ru")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, Unavailable
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return 0, nil, nil, Unavailable
	}
	if int64(len(body)) > maxBytes {
		return 0, nil, nil, ResponseChanged
	}
	return response.StatusCode, response.Header, body, nil
}
