package instinct

import (
	"sort"
	"unicode/utf8"
)

// EscalationRule is a threshold the *caller* supplies. HowlInstinct evaluates
// it and reports the result; it never invents one.
//
// This type is the whole of HowlInstinct's involvement in policy, and it is
// deliberately inert: satisfying or failing a rule changes an Outcome and a
// boolean, and nothing else. No rule can cause an action, a retry, a
// different provider, or a different answer.
type EscalationRule struct {
	// MinProviderConfidence gates on the confidence the provider itself
	// reported. If the provider reports no confidence (as is the case for a
	// noul, where the probability is the uncertainty signal), the rule cannot
	// be evaluated and the judgment is escalated with
	// ReasonConfidenceUnavailable rather than being silently passed or
	// silently failed.
	MinProviderConfidence *float64 `json:"min_provider_confidence,omitempty"`

	// MinInstinctMargin gates on InstinctMargin, a quantity HowlInstinct
	// derives itself. It is named separately from provider confidence so the
	// two can never be confused at a call site. See Judgment.InstinctMargin
	// for the formula.
	MinInstinctMargin *float64 `json:"min_instinct_margin,omitempty"`
}

// IsZero reports whether the rule constrains nothing.
func (r EscalationRule) IsZero() bool {
	return r.MinProviderConfidence == nil && r.MinInstinctMargin == nil
}

// Request is one call into HowlInstinct: some state, and a set of independent
// bounded questions about it.
type Request struct {
	// State is the material the questions are asked about. It is data, never
	// instructions: adapters carry it in its own field and never concatenate
	// it into a question's instructions.
	State string `json:"state"`

	// Questions are keyed by caller-supplied identifiers, which are preserved
	// exactly and echoed in every judgment and receipt.
	Questions map[string]Question `json:"questions"`

	// Escalation optionally supplies a per-question rule. A key here that
	// does not name a question is rejected rather than ignored, because a
	// misspelled key would silently disable the caller's own gate.
	Escalation map[string]EscalationRule `json:"escalation,omitempty"`

	// CorrelationID and WorkflowID are opaque caller identifiers carried
	// through into logs and receipts for tracing.
	CorrelationID string `json:"correlation_id,omitempty"`
	WorkflowID    string `json:"workflow_id,omitempty"`

	// RetainState opts in to persisting the raw state in the decision
	// receipt. It is off by default because state routinely contains logs,
	// customer data, or secrets, and a receipt is a durable artifact.
	RetainState bool `json:"retain_state,omitempty"`
}

// QuestionIDs returns the request's question identifiers in sorted order.
// Sorting is what makes downstream iteration deterministic; Go map order is
// randomized, and a receipt hash must not depend on it.
func (r Request) QuestionIDs() []string {
	ids := make([]string, 0, len(r.Questions))
	for id := range r.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Validate checks the whole request against the supplied limits.
func (r Request) Validate(lim Limits) error {
	if r.State == "" {
		return Errorf(KindInvalidInput, "validate", "state is empty")
	}
	if !utf8.ValidString(r.State) {
		return Errorf(KindInvalidInput, "validate", "state is not valid UTF-8")
	}
	if len(r.State) > lim.MaxStateBytes {
		return Errorf(KindInvalidInput, "validate",
			"state is %d bytes, limit is %d", len(r.State), lim.MaxStateBytes)
	}
	if len(r.Questions) == 0 {
		return Errorf(KindInvalidInput, "validate", "request contains no questions")
	}
	if len(r.Questions) > lim.MaxQuestions {
		return Errorf(KindInvalidInput, "validate",
			"request contains %d questions, limit is %d", len(r.Questions), lim.MaxQuestions)
	}
	for _, id := range r.QuestionIDs() {
		if err := ValidateID(id, lim); err != nil {
			return err
		}
		if err := r.Questions[id].Validate(id, lim); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(r.Escalation) {
		if _, ok := r.Questions[id]; !ok {
			return Errorf(KindInvalidInput, "validate",
				"escalation rule names unknown question %q", id)
		}
		if err := r.Escalation[id].validate(id); err != nil {
			return err
		}
	}
	for _, f := range []struct{ what, val string }{
		{"correlation_id", r.CorrelationID},
		{"workflow_id", r.WorkflowID},
	} {
		if f.val == "" {
			continue
		}
		if err := ValidateID(f.val, lim); err != nil {
			return Errorf(KindInvalidInput, "validate", "%s is invalid: %v", f.what, err)
		}
	}
	return nil
}

func (r EscalationRule) validate(id string) error {
	for _, f := range []struct {
		what string
		val  *float64
	}{
		{"min_provider_confidence", r.MinProviderConfidence},
		{"min_instinct_margin", r.MinInstinctMargin},
	} {
		if f.val == nil {
			continue
		}
		if !isFinite(*f.val) {
			return Errorf(KindInvalidInput, "validate",
				"escalation rule for %q has non-finite %s", id, f.what)
		}
		if *f.val < 0 || *f.val > 1 {
			return Errorf(KindInvalidInput, "validate",
				"escalation rule for %q has %s %v outside [0,1]", id, f.what, *f.val)
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
