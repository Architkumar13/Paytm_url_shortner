package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeURL_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://example.com", "https://example.com"},
		{"http://example.com/a/b?x=1", "http://example.com/a/b?x=1"},
		{"  https://example.com/x  ", "https://example.com/x"},
		{"HTTPS://Example.COM/Path", "https://example.com/Path"}, // scheme+host lowercased, path preserved
		{"https://example.com:8443/p", "https://example.com:8443/p"},
	}
	for _, c := range cases {
		got, err := NormalizeURL(c.in)
		if err != nil {
			t.Errorf("NormalizeURL(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeURL_Invalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"not-a-url",
		"ftp://example.com",
		"javascript:alert(1)",
		"mailto:a@b.com",
		"//example.com", // scheme-relative
		"http://",       // no host
		"https://" + strings.Repeat("a", 3000) + ".com", // too long
	}
	for _, c := range cases {
		if _, err := NormalizeURL(c); err == nil {
			t.Errorf("NormalizeURL(%q) expected error, got nil", c)
		} else {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("NormalizeURL(%q) error is not *ValidationError: %v", c, err)
			}
		}
	}
}

func TestAlias(t *testing.T) {
	valid := []string{"promo", "my-link_1", "abcDEF123"}
	for _, a := range valid {
		if err := Alias(a); err != nil {
			t.Errorf("Alias(%q) unexpected error: %v", a, err)
		}
	}

	invalid := []string{"", "ab", "has space", "bad!", "with/slash", "shorten", "API", strings.Repeat("x", 65)}
	for _, a := range invalid {
		if err := Alias(a); err == nil {
			t.Errorf("Alias(%q) expected error, got nil", a)
		}
	}
}
