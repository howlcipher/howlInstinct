package mock

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

func request() instinct.Request {
	return instinct.Request{
		State: "All payment requests are returning HTTP 500 across every region.",
		Questions: map[string]instinct.Question{
			"urgent": {Type: instinct.TypeNoul, Instructions: "Is this urgent?"},
			"category": {Type: instinct.TypeChoice, Instructions: "Category?",
				Options: []instinct.Option{
					{Name: "outage", Description: "requests returning errors across regions"},
					{Name: "billing", Description: "invoices and refunds"},
				}},
			"severity": {Type: instinct.TypeScore, Instructions: "How severe?",
				Levels: []instinct.Level{{Name: "low"}, {Name: "high"}}},
		},
	}
}

// The mock is the default provider and the backbone of the offline test
// suite. If it ever became non-deterministic, every test that depends on it
// becomes flaky in a way that is painful to diagnose.
func TestDecideIsDeterministic(t *testing.T) {
	first, err := New().Decide(context.Background(), request())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	canonical := func(r instinct.Response) string {
		raw, err := json.Marshal(r.Judgments)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		return string(raw)
	}
	want := canonical(first)
	for i := 0; i < 50; i++ {
		again, err := New().Decide(context.Background(), request())
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if got := canonical(again); got != want {
			t.Fatalf("mock output varied between runs:\n  %s\n  %s", want, got)
		}
	}
}

// The mock deliberately reproduces the real contract's asymmetry: choice and
// score carry a confidence, a noul does not. Reproducing the absence offline
// is what keeps the rest of the system honest about handling it.
func TestNoulCarriesNoConfidenceButOtherTypesDo(t *testing.T) {
	resp, err := New().Decide(context.Background(), request())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if c := resp.Judgments["urgent"].ProviderConfidence; c != nil {
		t.Errorf("noul reported confidence %v; the contract defines none", *c)
	}
	for _, id := range []string{"category", "severity"} {
		if resp.Judgments[id].ProviderConfidence == nil {
			t.Errorf("%s reported no confidence, but its type defines one", id)
		}
	}
}

// Every distribution must be a real distribution, or the validation and
// metric code downstream is being tested against input it will never see.
func TestDistributionsAreWellFormed(t *testing.T) {
	resp, err := New().Decide(context.Background(), request())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	for id, j := range resp.Judgments {
		if len(j.Probabilities) == 0 {
			continue
		}
		var sum float64
		for name, p := range j.Probabilities {
			if !instinct.IsFinite(p) {
				t.Fatalf("%s: probability for %q is not finite", id, name)
			}
			if p < 0 || p > 1 {
				t.Errorf("%s: probability for %q = %v, outside [0,1]", id, name, p)
			}
			sum += p
		}
		if diff := sum - 1; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s: probabilities sum to %v, not 1", id, sum)
		}
	}
}

// A word counter should never claim certainty. Saturating at 0 or 1 would
// make the baseline look more confident than any real provider.
func TestNoulProbabilityNeverSaturates(t *testing.T) {
	for _, state := range []string{
		"outage outage outage outage outage",
		"",
		"completely unrelated vocabulary zzz qqq",
	} {
		req := instinct.Request{
			State: state + " x",
			Questions: map[string]instinct.Question{
				"q": {Type: instinct.TypeNoul, Instructions: "Is this an outage?"},
			},
		}
		resp, err := New().Decide(context.Background(), req)
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		p := resp.Judgments["q"].ProbabilityYes
		if p == nil {
			t.Fatal("noul produced no probability")
		}
		if *p <= 0 || *p >= 1 {
			t.Fatalf("P(yes) = %v for state %q; a lexical baseline must not claim certainty", *p, state)
		}
	}
}

func TestScoreStaysWithinItsScale(t *testing.T) {
	levels := []instinct.Level{
		{Name: "negligible"}, {Name: "low"}, {Name: "moderate"}, {Name: "high"}, {Name: "critical"},
	}
	req := instinct.Request{
		State: "everything is on fire critical high moderate",
		Questions: map[string]instinct.Question{
			"sev": {Type: instinct.TypeScore, Instructions: "How severe?", Levels: levels},
		},
	}
	resp, err := New().Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	j := resp.Judgments["sev"]
	if j.Score == nil {
		t.Fatal("score judgment carried no score")
	}
	if *j.Score < 0 || *j.Score > float64(len(levels)-1) {
		t.Fatalf("score = %v, outside the scale [0,%d]", *j.Score, len(levels)-1)
	}
	if len(j.Legend) != len(levels) {
		t.Fatalf("legend has %d entries for %d levels", len(j.Legend), len(levels))
	}
}

// Classify must never surface as a fourth primitive in a provider's output.
func TestClassifyIsAnsweredAsAChoice(t *testing.T) {
	req := instinct.Request{
		State: "it crashes on save",
		Questions: map[string]instinct.Question{
			"cat": {Type: instinct.TypeClassify, Instructions: "Category?",
				Options: []instinct.Option{{Name: "bug"}, {Name: "feature"}}},
		},
	}
	resp, err := New().Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	j := resp.Judgments["cat"]
	if j.Choice == nil {
		t.Fatal("classify produced no choice")
	}
	if j.CompiledTo != instinct.TypeChoice {
		t.Errorf("compiled_to = %q, want %q", j.CompiledTo, instinct.TypeChoice)
	}
}

// Fault injection is what lets error handling be tested without a network.
func TestBehaviorInjection(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		boom := instinct.Errorf(instinct.KindProviderUnavailable, "mock", "no")
		_, err := NewWithBehavior(Behavior{FailWith: boom}).Decide(context.Background(), request())
		if err == nil {
			t.Fatal("Decide() = nil, want the injected failure")
		}
	})

	t.Run("dropped judgments", func(t *testing.T) {
		resp, err := NewWithBehavior(Behavior{OmitJudgments: []string{"urgent"}}).
			Decide(context.Background(), request())
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if _, ok := resp.Judgments["urgent"]; ok {
			t.Fatal("the omitted judgment was returned anyway")
		}
		if len(resp.Judgments) != 2 {
			t.Fatalf("got %d judgments, want 2", len(resp.Judgments))
		}
	})

	t.Run("scripted answers", func(t *testing.T) {
		resp, err := NewWithBehavior(Behavior{Scripted: map[string]instinct.Judgment{
			"urgent": {Type: instinct.TypeNoul, Outcome: instinct.OutcomeAcceptableConfidence,
				Bool: instinct.Bool(true), ProbabilityYes: instinct.Float(0.99)},
		}}).Decide(context.Background(), request())
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		j := resp.Judgments["urgent"]
		if j.ProbabilityYes == nil || *j.ProbabilityYes != 0.99 {
			t.Fatalf("scripted answer was not used: %+v", j)
		}
		if j.QuestionID != "urgent" {
			t.Errorf("scripted answer lost its question id: %q", j.QuestionID)
		}
	})

	t.Run("delay honours the deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := NewWithBehavior(Behavior{Delay: 2 * time.Second}).Decide(ctx, request())
		if err == nil {
			t.Fatal("Decide() = nil, want a timeout")
		}
		if kind, _ := instinct.KindOf(err); kind != instinct.KindTimeout {
			t.Errorf("error kind = %q, want %q", kind, instinct.KindTimeout)
		}
		if time.Since(start) > time.Second {
			t.Error("the delay outlived the deadline")
		}
	})
}

// The mock must describe itself honestly wherever it is named, so nobody
// mistakes a word counter for a model.
func TestModelNameIsSelfDescribing(t *testing.T) {
	if !strings.Contains(Model, "baseline") {
		t.Fatalf("Model = %q, want a name that does not read like a real model", Model)
	}
}
