package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

const secret = "sk-live-SUPERSECRET"

// Redaction is enforced in the handler, not at call sites, so this test
// deliberately logs a credential the way a careless future call site would.
func TestCredentialsNeverReachTheOutput(t *testing.T) {
	keys := []string{
		"api_key", "apikey", "API_KEY", "authorization", "Authorization",
		"token", "auth_token", "secret", "client_secret", "password",
		"credential", "bearer", "cookie",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			New(&buf, slog.LevelDebug).Info("calling provider", slog.String(key, secret))

			got := buf.String()
			if strings.Contains(got, secret) {
				t.Fatalf("logger emitted a credential under key %q: %s", key, got)
			}
			if !strings.Contains(got, Redacted) {
				t.Fatalf("logger did not mark %q as redacted: %s", key, got)
			}
		})
	}
}

// State is withheld for a different reason than a credential, and the output
// says which, so an operator reading a log knows whether something was
// secret or merely not retained.
func TestStateIsWithheldAndDistinguishedFromRedaction(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelDebug).Info("deciding",
		slog.String("state", "customer 4021 card ending 1234 declined"))

	got := buf.String()
	if strings.Contains(got, "4021") || strings.Contains(got, "1234") {
		t.Fatalf("logger emitted raw state: %s", got)
	}
	if !strings.Contains(got, "WITHHELD") {
		t.Fatalf("state was not marked as withheld: %s", got)
	}
}

// The fields that make a decision traceable must survive, or the logger is
// useless for the job it exists to do.
func TestProvenanceFieldsSurvive(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelDebug).Info("decided",
		slog.String("decision_id", "dec_abc"),
		slog.String("correlation_id", "corr-1"),
		slog.String("provider", "jev"),
		slog.String("model", "jev-latest"),
		slog.Int64("latency_ms", 42),
		slog.String("status", "ACCEPTABLE_CONFIDENCE"),
	)
	got := buf.String()
	for _, want := range []string{
		"dec_abc", "corr-1", "jev", "jev-latest", "42", "ACCEPTABLE_CONFIDENCE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log line dropped %q: %s", want, got)
		}
	}
}

// Nested groups must not provide a way around the filter.
func TestRedactionAppliesInsideGroups(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelDebug).Info("calling",
		slog.Group("provider", slog.String("api_key", secret)))

	if strings.Contains(buf.String(), secret) {
		t.Fatalf("a credential escaped through a group: %s", buf.String())
	}
}

func TestDiscardWritesNothing(t *testing.T) {
	// Mostly a guard that Discard stays wired to io.Discard: a logger that
	// quietly wrote to stderr would corrupt machine-readable CLI output.
	Discard().Error("this must not appear anywhere")
}
