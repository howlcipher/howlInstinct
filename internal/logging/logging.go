// Package logging provides HowlInstinct's structured logger.
//
// The logger is built around a single assumption: anything that reaches it
// may be sensitive, and the safe default is to withhold rather than to
// remember to redact. Two classes of value are therefore refused outright
// regardless of how they are passed, at the handler level rather than at call
// sites, because a rule enforced only at call sites is a rule that survives
// exactly until someone adds a new call site.
//
//   - Credentials, and anything whose key looks like one.
//   - Raw state, which routinely contains logs, customer records, or secrets.
//
// Output is JSON on stderr so that stdout remains clean for machine-readable
// results. The attribute names are chosen to be conventional enough for an
// OpenTelemetry exporter to map later without requiring one now.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted replaces any value withheld by the handler.
const Redacted = "[REDACTED]"

// sensitiveKeys are attribute names whose values are never logged.
var sensitiveKeys = []string{
	"authorization", "api_key", "apikey", "key", "token", "secret",
	"credential", "password", "bearer", "cookie", "set-cookie",
}

// withheldKeys are attribute names whose values are never logged because of
// what they contain rather than because they are secrets.
var withheldKeys = []string{"state", "raw_state"}

// New returns a structured logger writing JSON to w.
//
// Level is deliberately a parameter with no global default: a library that
// silently installs a global logger is a library that fights its host.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redact,
	}))
}

// Discard returns a logger that writes nothing, for tests and library use.
func Discard() *slog.Logger { return New(io.Discard, slog.LevelError) }

// redact is the handler-level filter. Because it runs on every attribute of
// every record, no call site can bypass it by accident.
func redact(_ []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)

	for _, withheld := range withheldKeys {
		if key == withheld {
			// Withheld rather than redacted, so a reader can tell the
			// difference between "this was a secret" and "this was the
			// judged material, which we do not persist".
			return slog.String(a.Key, "[WITHHELD: state is not logged]")
		}
	}
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(key, sensitive) {
			return slog.String(a.Key, Redacted)
		}
	}
	return a
}
