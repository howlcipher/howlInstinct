package decision

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/howlcipher/howlinstinct/internal/provider/mock"
	"github.com/howlcipher/howlinstinct/internal/receipt"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) }
}

func fixedIDs() receipt.IDFunc {
	n := 0
	return func() string {
		n++
		return "id-" + string(rune('a'+n-1))
	}
}

func testEngine(t *testing.T, p instinct.Provider, opts ...Option) *Engine {
	t.Helper()
	base := []Option{WithClock(fixedClock()), WithIDFunc(fixedIDs())}
	return New(p, append(base, opts...)...)
}

func incidentRequest() instinct.Request {
	return instinct.Request{
		State: "All payment requests are returning HTTP 500 across every region.",
		Questions: map[string]instinct.Question{
			"is_incident": {Type: instinct.TypeNoul, Instructions: "Is this an active production incident?"},
			"category": {Type: instinct.TypeClassify, Instructions: "Which category?",
				Options: []instinct.Option{
					{Name: "billing", Description: "invoices and payment charges"},
					{Name: "outage", Description: "service unavailable errors"},
					{Name: "feature", Description: "new functionality request"},
				}},
			"severity": {Type: instinct.TypeScore, Instructions: "How severe?",
				Levels: []instinct.Level{
					{Name: "negligible"}, {Name: "low"}, {Name: "moderate"},
					{Name: "high"}, {Name: "critical"},
				}},
		},
	}
}

func TestDecideAnswersEveryQuestionInABatch(t *testing.T) {
	e := testEngine(t, mock.New())
	res, err := e.Decide(context.Background(), incidentRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if len(res.Judgments()) != 3 {
		t.Fatalf("got %d judgments, want 3", len(res.Judgments()))
	}
	for _, id := range []string{"is_incident", "category", "severity"} {
		if _, ok := res.Response.Judgments[id]; !ok {
			t.Errorf("no judgment for question %q; question ids must be preserved exactly", id)
		}
	}
	if len(res.Receipts) != 3 {
		t.Fatalf("got %d receipts, want one per question", len(res.Receipts))
	}
	for _, r := range res.Receipts {
		if err := r.Validate(); err != nil {
			t.Errorf("receipt %q invalid: %v", r.QuestionID, err)
		}
		if r.BatchID != res.BatchID {
			t.Errorf("receipt %q has batch id %q, want %q", r.QuestionID, r.BatchID, res.BatchID)
		}
	}
}

// The provider must never be handed a classify question. This spies on what
// actually arrives at the adapter boundary.
func TestClassifyIsLoweredBeforeReachingTheProvider(t *testing.T) {
	spy := &spyProvider{inner: mock.New()}
	e := testEngine(t, spy)

	res, err := e.Decide(context.Background(), incidentRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	for id, q := range spy.seen.Questions {
		if q.Type == instinct.TypeClassify {
			t.Fatalf("provider received a classify question for %q; classify must lower to choice", id)
		}
	}
	if got := spy.seen.Questions["category"].Type; got != instinct.TypeChoice {
		t.Fatalf("provider saw type %q for the classify question, want %q", got, instinct.TypeChoice)
	}

	// ...and the caller's vocabulary must come back intact.
	j := res.Response.Judgments["category"]
	if j.Type != instinct.TypeClassify {
		t.Errorf("judgment type = %q, want the caller's %q", j.Type, instinct.TypeClassify)
	}
	if j.CompiledTo != instinct.TypeChoice {
		t.Errorf("judgment compiled_to = %q, want %q", j.CompiledTo, instinct.TypeChoice)
	}
}

// With no caller rule, nothing may be escalated no matter how weak the
// judgment is. This is the boundary the whole project exists to hold.
func TestNothingEscalatesWithoutACallerRule(t *testing.T) {
	e := testEngine(t, mock.New())
	// State that shares no vocabulary with any option: the mock will be
	// maximally unsure, which is exactly when a system with a hidden default
	// threshold would betray itself.
	req := incidentRequest()
	req.State = "zzz qqq vvv"

	res, err := e.Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	for id, j := range res.Response.Judgments {
		if j.NeedsEscalation {
			t.Errorf("question %q escalated with no caller rule (reason %q)", id, j.EscalationReason)
		}
		if j.Outcome != instinct.OutcomeAcceptableConfidence {
			t.Errorf("question %q outcome = %q, want %q", id, j.Outcome, instinct.OutcomeAcceptableConfidence)
		}
	}
}

func TestCallerRuleDrivesEscalation(t *testing.T) {
	e := testEngine(t, mock.New())
	req := incidentRequest()
	req.State = "zzz qqq vvv"
	req.Escalation = map[string]instinct.EscalationRule{
		// An impossible bar, so the rule must fire.
		"category": {MinInstinctMargin: instinct.Float(0.99)},
	}

	res, err := e.Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	cat := res.Response.Judgments["category"]
	if !cat.NeedsEscalation {
		t.Fatal("category did not escalate despite a caller rule it cannot meet")
	}
	if cat.EscalationReason != instinct.ReasonBelowCallerThreshold {
		t.Errorf("reason = %q, want %q", cat.EscalationReason, instinct.ReasonBelowCallerThreshold)
	}
	if cat.Outcome != instinct.OutcomeLowConfidence {
		t.Errorf("outcome = %q, want %q", cat.Outcome, instinct.OutcomeLowConfidence)
	}
	// A rule on one question must not leak onto another.
	if res.Response.Judgments["severity"].NeedsEscalation {
		t.Error("severity escalated although no rule named it")
	}
}

// Asking a noul to be gated on provider confidence cannot be satisfied,
// because a noul has none. The honest answer is to say so.
func TestConfidenceGateOnANoulReportsUnavailable(t *testing.T) {
	e := testEngine(t, mock.New())
	req := incidentRequest()
	req.Escalation = map[string]instinct.EscalationRule{
		"is_incident": {MinProviderConfidence: instinct.Float(0.5)},
	}
	res, err := e.Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	j := res.Response.Judgments["is_incident"]
	if j.ProviderConfidence != nil {
		t.Fatalf("a noul reported provider confidence %v; it should have none", *j.ProviderConfidence)
	}
	if j.EscalationReason != instinct.ReasonConfidenceUnavailable {
		t.Fatalf("reason = %q, want %q", j.EscalationReason, instinct.ReasonConfidenceUnavailable)
	}
}

func TestDecideRejectsAnInvalidRequestBeforeCallingTheProvider(t *testing.T) {
	spy := &spyProvider{inner: mock.New()}
	e := testEngine(t, spy)

	_, err := e.Decide(context.Background(), instinct.Request{State: ""})
	if err == nil {
		t.Fatal("Decide() = nil, want a validation error")
	}
	if kind, _ := instinct.KindOf(err); kind != instinct.KindInvalidInput {
		t.Errorf("error kind = %q, want %q", kind, instinct.KindInvalidInput)
	}
	if spy.calls != 0 {
		t.Errorf("provider was called %d times for an invalid request, want 0", spy.calls)
	}
}

// A failed decision still has to leave a record. Omitting the attempt would
// make the audit trail claim nothing was asked.
func TestProviderFailureStillProducesReceipts(t *testing.T) {
	boom := instinct.Errorf(instinct.KindProviderUnavailable, "mock", "connection refused")
	e := testEngine(t, mock.NewWithBehavior(mock.Behavior{FailWith: boom}))

	res, err := e.Decide(context.Background(), incidentRequest())
	if err == nil {
		t.Fatal("Decide() = nil error, want the provider failure to surface")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the provider's own error", err)
	}
	if len(res.Receipts) != 3 {
		t.Fatalf("got %d receipts for a failed decision, want one per question", len(res.Receipts))
	}
	for _, r := range res.Receipts {
		if r.Outcome != instinct.OutcomeUnavailable {
			t.Errorf("receipt %q outcome = %q, want %q", r.QuestionID, r.Outcome, instinct.OutcomeUnavailable)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("failure receipt %q is invalid: %v", r.QuestionID, err)
		}
	}
}

// An invalid provider response is an integrity problem, not an availability
// one, and the outcome must say so.
func TestInvalidProviderResponseIsClassifiedAsError(t *testing.T) {
	bad := instinct.Errorf(instinct.KindProviderResponseInvalid, "jev", "probabilities do not sum to 1")
	e := testEngine(t, mock.NewWithBehavior(mock.Behavior{FailWith: bad}))

	res, _ := e.Decide(context.Background(), incidentRequest())
	for _, r := range res.Receipts {
		if r.Outcome != instinct.OutcomeError {
			t.Errorf("receipt %q outcome = %q, want %q", r.QuestionID, r.Outcome, instinct.OutcomeError)
		}
	}
}

func TestDecideHonoursTheTimeout(t *testing.T) {
	e := testEngine(t,
		mock.NewWithBehavior(mock.Behavior{Delay: 2 * time.Second}),
		WithTimeout(25*time.Millisecond))

	start := time.Now()
	_, err := e.Decide(context.Background(), incidentRequest())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Decide() = nil, want a timeout")
	}
	if kind, _ := instinct.KindOf(err); kind != instinct.KindTimeout {
		t.Errorf("error kind = %q, want %q", kind, instinct.KindTimeout)
	}
	if elapsed > time.Second {
		t.Errorf("Decide() took %v; the deadline was not enforced", elapsed)
	}
}

func TestDecideRespectsAnAlreadyCancelledContext(t *testing.T) {
	e := testEngine(t, mock.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := e.Decide(ctx, incidentRequest()); err == nil {
		t.Fatal("Decide() = nil, want a cancellation error")
	}
}

// A provider that drops part of a batch must not silently shrink the result.
func TestDroppedJudgmentBecomesAnUnavailableReceipt(t *testing.T) {
	e := testEngine(t, mock.NewWithBehavior(mock.Behavior{OmitJudgments: []string{"severity"}}))

	res, err := e.Decide(context.Background(), incidentRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if len(res.Receipts) != 3 {
		t.Fatalf("got %d receipts, want 3 even though the provider answered 2", len(res.Receipts))
	}
	for _, r := range res.Receipts {
		if r.QuestionID != "severity" {
			continue
		}
		if r.Outcome != instinct.OutcomeUnavailable {
			t.Errorf("dropped question outcome = %q, want %q", r.Outcome, instinct.OutcomeUnavailable)
		}
		if !strings.Contains(r.Error, "no judgment") {
			t.Errorf("dropped question error = %q, want it to explain the omission", r.Error)
		}
	}
}

// The mock is the default provider precisely because it needs nothing. If it
// ever became non-deterministic the entire offline test story collapses.
func TestMockProviderIsDeterministic(t *testing.T) {
	e := testEngine(t, mock.New())
	first, err := e.Decide(context.Background(), incidentRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := testEngine(t, mock.New()).Decide(context.Background(), incidentRequest())
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		for id, want := range first.Response.Judgments {
			got := again.Response.Judgments[id]
			if !sameJudgment(want, got) {
				t.Fatalf("question %q varied between runs:\n  %+v\n  %+v", id, want, got)
			}
		}
	}
}

func sameJudgment(a, b instinct.Judgment) bool {
	if a.Outcome != b.Outcome || a.Type != b.Type {
		return false
	}
	if !sameFloat(a.ProbabilityYes, b.ProbabilityYes) ||
		!sameFloat(a.Score, b.Score) ||
		!sameFloat(a.ProviderConfidence, b.ProviderConfidence) ||
		!sameFloat(a.InstinctMargin, b.InstinctMargin) {
		return false
	}
	if (a.Choice == nil) != (b.Choice == nil) {
		return false
	}
	if a.Choice != nil && *a.Choice != *b.Choice {
		return false
	}
	if len(a.Probabilities) != len(b.Probabilities) {
		return false
	}
	for k, v := range a.Probabilities {
		if b.Probabilities[k] != v {
			return false
		}
	}
	return true
}

func sameFloat(a, b *float64) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

type spyProvider struct {
	inner instinct.Provider
	seen  instinct.Request
	calls int
}

func (s *spyProvider) Name() string { return s.inner.Name() }

func (s *spyProvider) Decide(ctx context.Context, req instinct.Request) (instinct.Response, error) {
	s.seen = req
	s.calls++
	return s.inner.Decide(ctx, req)
}
