//go:build live

// These tests talk to a real provider and are excluded from every gate.
//
// A build tag rather than an environment-variable check, because a tag cannot
// be satisfied by accident: a variable that happens to be set in someone's
// shell should never be enough to start spending money and sending state to a
// remote service during an ordinary `go test ./...`.
//
// Run them deliberately:
//
//	export HOWLINSTINCT_LIVE_BASE_URL="https://api.example.com"
//	export HOWLINSTINCT_LIVE_API_KEY_ENV="MY_PROVIDER_API_KEY"
//	export HOWLINSTINCT_LIVE_MODEL="jev-latest"     # optional
//	export MY_PROVIDER_API_KEY="..."
//	go test -tags live ./internal/provider/jev/
//
// These are smoke tests, not correctness tests. They check that a real
// endpoint speaks the contract this adapter implements. They deliberately do
// not assert on the content of any judgment, because a remote model's answer
// is not a fixed value and a test that pins one would be asserting that the
// provider never improves.
package jev

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

func liveProvider(t *testing.T) *Provider {
	t.Helper()

	base := os.Getenv("HOWLINSTINCT_LIVE_BASE_URL")
	if base == "" {
		t.Skip("HOWLINSTINCT_LIVE_BASE_URL is not set")
	}
	cfg := DefaultConfig()
	cfg.BaseURL = base
	cfg.APIKeyEnv = os.Getenv("HOWLINSTINCT_LIVE_API_KEY_ENV")
	cfg.Model = os.Getenv("HOWLINSTINCT_LIVE_MODEL")
	cfg.Timeout = 30 * time.Second

	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return p
}

// A single call carrying all three primitives, which is the shape most likely
// to expose a contract mismatch.
func TestLiveAnswersAllThreePrimitives(t *testing.T) {
	p := liveProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := p.Decide(ctx, batchRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	for _, id := range []string{"urgent", "category", "severity"} {
		j, ok := resp.Judgments[id]
		if !ok {
			t.Fatalf("no judgment for %q", id)
		}
		if j.Outcome != instinct.OutcomeAcceptableConfidence {
			t.Errorf("%s outcome = %q", id, j.Outcome)
		}
	}

	if resp.Judgments["urgent"].ProbabilityYes == nil {
		t.Error("noul returned no probability")
	}
	if resp.Judgments["category"].Choice == nil {
		t.Error("choice returned no selection")
	}
	if resp.Judgments["severity"].Score == nil {
		t.Error("score returned no value")
	}

	// Not an assertion that a noul has no confidence, because at least one
	// implementation of this contract does report one. It is recorded so that
	// running this against a real endpoint tells us which behaviour it has.
	if c := resp.Judgments["urgent"].ProviderConfidence; c != nil {
		t.Logf("note: this endpoint reports a confidence for noul (%v); "+
			"the contract does not define one", *c)
	} else {
		t.Log("this endpoint reports no confidence for noul, as the contract describes")
	}
}

// Strict schema checking is the setting most likely to break against a real
// endpoint that has moved on, so a live run should say so clearly rather than
// failing with a generic decode error.
func TestLiveStrictSchemaStillMatches(t *testing.T) {
	p := liveProvider(t)
	if !p.Config().StrictSchema {
		t.Skip("strict schema is disabled in this configuration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := p.Decide(ctx, batchRequest()); err != nil {
		if kind, _ := instinct.KindOf(err); kind == instinct.KindProviderResponseInvalid {
			t.Fatalf("the live endpoint's response no longer matches this adapter's "+
				"expectations, which means the wire contract has drifted: %v", err)
		}
		t.Fatalf("Decide() error = %v", err)
	}
}
