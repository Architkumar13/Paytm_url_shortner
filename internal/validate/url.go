// Package validate contains input validation for URLs and custom aliases.
package validate

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	// MaxURLLength caps stored URLs. 2048 matches the de-facto browser limit.
	MaxURLLength = 2048
	// MinAliasLength / MaxAliasLength bound user-supplied custom aliases.
	MinAliasLength = 3
	MaxAliasLength = 64
)

// reservedAliases must never be usable as custom codes because they would
// shadow fixed API routes (see internal/httpapi).
var reservedAliases = map[string]bool{
	"shorten": true,
	"healthz": true,
	"api":     true,
}

// ValidationError marks an error as caused by bad client input. The HTTP layer
// maps any ValidationError to 400 Bad Request via errors.As, so validation
// rules stay in one place instead of being duplicated across handlers.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

func verr(format string, args ...any) *ValidationError {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// NormalizeURL validates raw and returns a canonical form suitable for storage
// and de-duplication.
//
// Decisions (documented deliberately):
//   - Only absolute http/https URLs are accepted. This rejects dangerous schemes
//     such as javascript:, mailto:, file: and relative references.
//   - Scheme and host are lowercased so that https://Example.com and
//     https://example.com de-duplicate to a single mapping.
//   - Path and query are preserved verbatim because they can be case-sensitive.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", verr("url is required")
	}
	if len(raw) > MaxURLLength {
		return "", verr("url exceeds %d characters", MaxURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", verr("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", verr("url must use http or https scheme")
	}
	if u.Host == "" {
		return "", verr("url must include a host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

// Alias validates a user-supplied custom alias. Allowed characters are the same
// URL-safe set used for generated codes plus '-' and '_'.
func Alias(alias string) error {
	if len(alias) < MinAliasLength || len(alias) > MaxAliasLength {
		return verr("alias must be between %d and %d characters", MinAliasLength, MaxAliasLength)
	}
	for i := 0; i < len(alias); i++ {
		c := alias[i]
		ok := (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			c == '-' || c == '_'
		if !ok {
			return verr("alias may only contain letters, digits, '-' and '_'")
		}
	}
	if reservedAliases[strings.ToLower(alias)] {
		return verr("alias %q is reserved", alias)
	}
	return nil
}
