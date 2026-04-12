package utils

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	nonDigit    = regexp.MustCompile(`\D`)
	multiSpace  = regexp.MustCompile(`\s+`)
	punctuation = regexp.MustCompile(`[^\w\s]`)
)

// NormalizeEmail lowercases, trims whitespace, and strips Gmail-style + aliases.
func NormalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	// Strip Gmail + alias: user+tag@gmail.com → user@gmail.com
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		local := strings.SplitN(parts[0], "+", 2)[0]
		email = local + "@" + parts[1]
	}
	return email
}

// NormalizePhone strips all non-digit characters and returns the digits only.
// For scoring purposes (not display).
func NormalizePhone(phone string) string {
	return nonDigit.ReplaceAllString(phone, "")
}

// NormalizeName lowercases, collapses whitespace, and removes punctuation for comparison.
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = punctuation.ReplaceAllString(name, "")
	name = multiSpace.ReplaceAllString(name, " ")
	// Remove accents (basic pass — strips combining characters)
	var b strings.Builder
	for _, r := range name {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// EmailDomain extracts the domain part of an email address.
func EmailDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
