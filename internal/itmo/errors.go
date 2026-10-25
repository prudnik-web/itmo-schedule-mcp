package itmo

import "errors"

// Error contains only a public category. Never attach HTTP bodies, URLs or causes:
// net/http and JSON errors can include credentials, codes, or personal data.
type Error string

const (
	InvalidInput           Error = "invalid_input"
	MissingCredentials     Error = "missing_credentials"
	AuthenticationRequired Error = "itmo_authentication_required"
	Unavailable            Error = "itmo_unavailable"
	ResponseChanged        Error = "itmo_response_changed"
	RateLimited            Error = "rate_limited"
	// Unauthorized is internal; the session layer consumes it before replying.
	Unauthorized Error = "itmo_token_rejected"
)

func (e Error) Error() string { return string(e) }

func PublicError(err error) Error {
	var e Error
	if errors.As(err, &e) {
		switch e {
		case InvalidInput, MissingCredentials, AuthenticationRequired, Unavailable, ResponseChanged, RateLimited:
			return e
		case Unauthorized:
			return AuthenticationRequired
		}
	}
	return Unavailable
}
