package itmo

import "strings"

// Diagnostic contains only locally chosen labels and HTTP statuses. It never
// contains an upstream string, URL, body, credential, cookie or token.
type Diagnostic struct {
	Step   string `json:"step"`
	Status int    `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ObserveDiagnostics is for the private SSH verification command only. Set it
// before using the client. The public MCP service does not install an observer.
func (c *Client) ObserveDiagnostics(observer func(Diagnostic)) { c.observe = observer }

func (c *Client) diagnostic(step string, status int, detail string) {
	if c.observe != nil {
		c.observe(Diagnostic{Step: step, Status: status, Detail: detail})
	}
}

func pageCategory(body []byte) string {
	page := strings.ToLower(string(body))
	for _, category := range []struct {
		name    string
		markers []string
	}{
		{"interactive_challenge", []string{"login-otp.ftl", "login-webauthn.ftl", "name=\"otp\"", "name=\"totp\"", "g-recaptcha"}},
		{"invalid_credentials", []string{"invalidusermessage", "invalid username or password", "неверный логин или пароль", "неверное имя пользователя или пароль", "неправильное имя пользователя или пароль"}},
		{"session_expired", []string{"expiredcodemessage", "login-page-expired.ftl", "cookie not found", "cookie_not_found", "session expired"}},
		{"account_locked", []string{"accounttemporarilydisabledmessage", "accountdisabledmessage", "account is disabled", "account is temporarily disabled"}},
	} {
		for _, marker := range category.markers {
			if strings.Contains(page, marker) {
				return category.name
			}
		}
	}
	return "unrecognized_login_response"
}
