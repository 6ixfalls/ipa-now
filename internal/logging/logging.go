// Package logging owns the process logger and bounded, redacted operator
// diagnostics. Logs go to the operator's stderr only; they never feed API
// responses or durable job records.
package logging

import (
	"errors"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
)

var discard = slog.New(slog.DiscardHandler)

// OrDiscard returns l, or a shared no-op logger when l is nil, so components
// without a configured logger (tests) stay silent.
func OrDiscard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return discard
	}
	return l
}

// New builds the process logger on stderr from parsed configuration.
func New(level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

const detailLimit = 2000

var secretPair = regexp.MustCompile(`(?i)\b(password|passphrase|secret|token|authorization|cookie|session)\b\s*[:=]\s*(bearer\s+[^\s;,)]+|"[^"]*"|[^\s;,)]+)`)
var bearer = regexp.MustCompile(`(?i)\bbearer\s+[^\s;,)]+`)

// Redact masks credential-like key/value pairs and bearer tokens in
// diagnostic text. It is a safety net over library error strings, which may
// quote request material; it is not a promise that all secrets are found.
func Redact(s string) string {
	s = secretPair.ReplaceAllStringFunc(s, func(m string) string {
		sep := strings.IndexAny(m, ":=")
		return m[:sep+1] + " [redacted]"
	})
	return bearer.ReplaceAllString(s, "bearer [redacted]")
}

// Detail renders untrusted diagnostic text as one bounded, redacted line.
func Detail(s string) string {
	s = strings.NewReplacer("\r\n", "; ", "\r", "; ", "\n", "; ").Replace(s)
	s = Redact(s)
	if len(s) > detailLimit {
		s = s[:detailLimit] + " ...[truncated]"
	}
	return s
}

// Fields renders only explicitly allowed untrusted diagnostic fields.
// Callers must maintain the allowlist at the boundary that owns the data.
func Fields(fields map[string]string, allow ...string) string {
	if len(fields) == 0 || len(allow) == 0 {
		return ""
	}
	keys := make([]string, 0, len(allow))
	for _, key := range allow {
		if _, ok := fields[key]; ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+fields[key])
	}
	return Detail(strings.Join(pairs, " "))
}

// Chain renders err and its wrapped causes as one bounded, redacted line.
// Library causes are operator diagnostics; the user-safe text is derived
// separately from the stable code.
func Chain(err error) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	for err != nil {
		msg := strings.NewReplacer("\r\n", "; ", "\r", "; ", "\n", "; ").Replace(err.Error())
		if len(parts) == 0 || parts[len(parts)-1] != msg {
			parts = append(parts, msg)
		}
		err = errors.Unwrap(err)
	}
	return Detail(strings.Join(parts, ": "))
}
