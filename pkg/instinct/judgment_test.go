package instinct

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// This is the load-bearing serialization test for the whole project.
//
// A noul carries no provider confidence. If Judgment stored confidence as a
// plain float64, an absent confidence would serialize as 0.0, which asserts
// that the provider was maximally unconfident. It never said that. The field
// must be absent from the JSON entirely.
func TestAbsentProviderConfidenceSerializesAsAbsentNotZero(t *testing.T) {
	j := Judgment{
		QuestionID:     "urgent",
		Type:           TypeNoul,
		Outcome:        OutcomeAcceptableConfidence,
		Bool:           Bool(true),
		ProbabilityYes: Float(0.98),
		// ProviderConfidence deliberately left nil: a noul has none.
	}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got := string(raw)
	if strings.Contains(got, "provider_confidence") {
		t.Fatalf("noul judgment serialized a provider_confidence field: %s", got)
	}

	// And the round trip must not manufacture one either.
	var back Judgment
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if back.ProviderConfidence != nil {
		t.Fatalf("round trip produced provider_confidence = %v, want nil", *back.ProviderConfidence)
	}
}

// A provider that genuinely reports zero confidence must be distinguishable
// from one that reports none. This is the other half of the pointer contract.
func TestZeroProviderConfidenceIsPreservedAndDistinctFromAbsent(t *testing.T) {
	j := Judgment{QuestionID: "q", Type: TypeChoice, ProviderConfidence: Float(0)}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"provider_confidence":0`) {
		t.Fatalf("explicit zero confidence was dropped: %s", raw)
	}
}

func TestMarginFromProbabilityYes(t *testing.T) {
	tests := []struct {
		name string
		p    float64
		want float64
		ok   bool
	}{
		{"certain yes", 1.0, 1.0, true},
		{"certain no", 0.0, 1.0, true},
		{"coin flip", 0.5, 0.0, true},
		{"leaning yes", 0.75, 0.5, true},
		{"leaning no", 0.25, 0.5, true},
		{"NaN rejected", math.NaN(), 0, false},
		{"Inf rejected", math.Inf(1), 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MarginFromProbabilityYes(tc.p)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("margin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMarginFromDistribution(t *testing.T) {
	tests := []struct {
		name string
		dist map[string]float64
		want float64
		ok   bool
	}{
		{"decisive", map[string]float64{"a": 0.99, "b": 0.01}, 0.98, true},
		{"split", map[string]float64{"a": 0.5, "b": 0.5}, 0.0, true},
		{"three way", map[string]float64{"a": 0.6, "b": 0.3, "c": 0.1}, 0.3, true},
		{"single entry has no runner up", map[string]float64{"a": 1.0}, 1.0, true},
		{"empty", map[string]float64{}, 0, false},
		{"NaN rejected", map[string]float64{"a": math.NaN(), "b": 0.5}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MarginFromDistribution(tc.dist)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("margin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplyEscalation(t *testing.T) {
	tests := []struct {
		name       string
		judgment   Judgment
		rule       EscalationRule
		wantOut    Outcome
		wantEsc    bool
		wantReason string
	}{
		{
			name:     "no rule never escalates even at a coin flip",
			judgment: Judgment{ProbabilityYes: Float(0.5), InstinctMargin: Float(0)},
			rule:     EscalationRule{},
			wantOut:  OutcomeAcceptableConfidence,
		},
		{
			name:     "confidence above caller threshold",
			judgment: Judgment{ProviderConfidence: Float(0.9)},
			rule:     EscalationRule{MinProviderConfidence: Float(0.8)},
			wantOut:  OutcomeAcceptableConfidence,
		},
		{
			name:       "confidence below caller threshold",
			judgment:   Judgment{ProviderConfidence: Float(0.7)},
			rule:       EscalationRule{MinProviderConfidence: Float(0.8)},
			wantOut:    OutcomeLowConfidence,
			wantEsc:    true,
			wantReason: ReasonBelowCallerThreshold,
		},
		{
			// The noul case: the caller asked to gate on provider confidence,
			// but a noul has none. Reporting it as unavailable is the honest
			// answer; substituting the probability would be inventing data.
			name:       "confidence gate with no provider confidence is unavailable",
			judgment:   Judgment{Type: TypeNoul, ProbabilityYes: Float(0.99)},
			rule:       EscalationRule{MinProviderConfidence: Float(0.8)},
			wantOut:    OutcomeLowConfidence,
			wantEsc:    true,
			wantReason: ReasonConfidenceUnavailable,
		},
		{
			name:     "margin above caller threshold",
			judgment: Judgment{InstinctMargin: Float(0.96)},
			rule:     EscalationRule{MinInstinctMargin: Float(0.5)},
			wantOut:  OutcomeAcceptableConfidence,
		},
		{
			name:       "margin below caller threshold",
			judgment:   Judgment{InstinctMargin: Float(0.1)},
			rule:       EscalationRule{MinInstinctMargin: Float(0.5)},
			wantOut:    OutcomeLowConfidence,
			wantEsc:    true,
			wantReason: ReasonBelowCallerThreshold,
		},
		{
			name:       "margin gate with no margin is unavailable",
			judgment:   Judgment{},
			rule:       EscalationRule{MinInstinctMargin: Float(0.5)},
			wantOut:    OutcomeLowConfidence,
			wantEsc:    true,
			wantReason: ReasonConfidenceUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := tc.judgment
			j.ApplyEscalation(tc.rule)
			if j.Outcome != tc.wantOut {
				t.Errorf("Outcome = %q, want %q", j.Outcome, tc.wantOut)
			}
			if j.NeedsEscalation != tc.wantEsc {
				t.Errorf("NeedsEscalation = %v, want %v", j.NeedsEscalation, tc.wantEsc)
			}
			if j.EscalationReason != tc.wantReason {
				t.Errorf("EscalationReason = %q, want %q", j.EscalationReason, tc.wantReason)
			}
		})
	}
}

// A failed judgment must not be promoted to acceptable by escalation logic.
func TestApplyEscalationLeavesFailedOutcomesAlone(t *testing.T) {
	for _, out := range []Outcome{OutcomeUnavailable, OutcomeError} {
		t.Run(string(out), func(t *testing.T) {
			j := Judgment{Outcome: out, Error: "provider exploded"}
			j.ApplyEscalation(EscalationRule{MinInstinctMargin: Float(0.1)})
			if j.Outcome != out {
				t.Fatalf("Outcome = %q, want it left as %q", j.Outcome, out)
			}
		})
	}
}

// HowlInstinct must hold no opinion about what counts as confident enough.
// Every escalation reason it can emit must be traceable to a caller rule.
func TestEveryEscalationReasonRequiresACallerRule(t *testing.T) {
	samples := []Judgment{
		{ProbabilityYes: Float(0.5), InstinctMargin: Float(0)},
		{ProviderConfidence: Float(0.01), InstinctMargin: Float(0.001)},
		{Type: TypeScore, Score: Float(2), InstinctMargin: Float(0)},
	}
	for i, j := range samples {
		j.ApplyEscalation(EscalationRule{})
		if j.NeedsEscalation || j.EscalationReason != "" {
			t.Errorf("sample %d escalated with no caller rule: reason %q", i, j.EscalationReason)
		}
		if j.Outcome != OutcomeAcceptableConfidence {
			t.Errorf("sample %d outcome = %q, want %q", i, j.Outcome, OutcomeAcceptableConfidence)
		}
	}
}

func TestOutcomeIsValid(t *testing.T) {
	for _, o := range []Outcome{
		OutcomeAcceptableConfidence, OutcomeLowConfidence, OutcomeUnavailable, OutcomeError,
	} {
		if !o.IsValid() {
			t.Errorf("%q should be valid", o)
		}
	}
	for _, o := range []Outcome{"", "APPROVED", "DENIED", "acceptable_confidence"} {
		if o.IsValid() {
			t.Errorf("%q should not be valid", o)
		}
	}
}
