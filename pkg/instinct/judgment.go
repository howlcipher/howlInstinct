package instinct

import (
	"context"
	"math"
	"sort"
)

// Usage reports provider-side token accounting when the provider supplies it.
// Both fields are pointers so that "the provider reported zero output tokens"
// and "the provider reported nothing" stay distinguishable.
type Usage struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
}

// Judgment is HowlInstinct's answer to one bounded question.
//
// Every value a provider supplied is a pointer. That is not stylistic. A
// missing value and a zero value mean entirely different things here: a noul
// carries no provider confidence at all, and serializing that absence as 0.0
// would assert that the provider was maximally unconfident, which it never
// said. Pointers make "the provider did not report this" representable, and
// make it impossible to satisfy a confidence threshold with a value nobody
// produced.
type Judgment struct {
	QuestionID string `json:"question_id"`

	// Type is what the caller asked for. CompiledTo is what the provider was
	// actually asked, and is set only when the two differ (classify lowers to
	// choice). Keeping both preserves provenance across the lowering.
	Type       DecisionType `json:"type"`
	CompiledTo DecisionType `json:"compiled_to,omitempty"`

	Outcome Outcome `json:"outcome"`

	// Exactly one of these is set on a successful judgment, according to type.
	Bool   *bool    `json:"bool,omitempty"`
	Choice *string  `json:"choice,omitempty"`
	Score  *float64 `json:"score,omitempty"`

	// Legend is the ordered level names of a score question: index i is the
	// score value i. It is echoed so a stored receipt stays interpretable
	// without the original request.
	Legend []string `json:"legend,omitempty"`

	// ProbabilityYes is the provider's P(yes) for a noul.
	ProbabilityYes *float64 `json:"probability_yes,omitempty"`

	// Probabilities is the provider's distribution for a choice or score,
	// keyed by option or level name.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`

	// ProviderConfidence is the confidence the provider itself reported. It
	// is named for its origin so it can never be mistaken for a quantity
	// HowlInstinct computed. It is absent whenever the provider reported none.
	ProviderConfidence *float64 `json:"provider_confidence,omitempty"`

	// InstinctMargin is derived by HowlInstinct, never by a provider. It is
	// how decisively the distribution favours the selected value, on [0,1]:
	//
	//   noul            |2*P(yes) - 1|      0 is a coin flip, 1 is decisive
	//   choice/classify p(top) - p(runner-up)
	//   score           p(modal level) - p(runner-up level)
	//
	// It is a restatement of the probabilities and nothing more. It is not a
	// calibration result, it is not an accuracy estimate, and it is not a
	// substitute for a confidence the provider declined to give.
	InstinctMargin *float64 `json:"instinct_margin,omitempty"`

	// NeedsEscalation is set only when the caller supplied an escalation rule
	// that was not satisfied or could not be evaluated.
	NeedsEscalation  bool   `json:"needs_escalation"`
	EscalationReason string `json:"escalation_reason,omitempty"`

	// Error carries a human-readable reason when Outcome is UNAVAILABLE or
	// ERROR. It never carries credentials or raw state.
	Error string `json:"error,omitempty"`
}

// Response is the result of one provider call.
type Response struct {
	Judgments map[string]Judgment `json:"judgments"`

	// Provider identifies the adapter. Model and Endpoint identify what it
	// talked to. Endpoint is always credential-free: see receipt.SafeEndpoint.
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`

	LatencyMS int64  `json:"latency_ms"`
	Usage     *Usage `json:"usage,omitempty"`
}

// Provider is the one interface every decision source implements.
//
// It is intentionally tiny and intentionally free of vendor vocabulary. An
// adapter translates a Request into whatever its endpoint speaks, validates
// what comes back, and returns judgments. It does not decide policy, does not
// consult a threshold, and does not escalate: escalation is evaluated above
// the provider layer, from the caller's own rule, so that no provider can
// quietly become the thing that decides what is confident enough.
type Provider interface {
	// Name returns a short stable adapter identifier, recorded in receipts.
	Name() string

	// Decide answers every question in req. It must honour ctx's deadline.
	Decide(ctx context.Context, req Request) (Response, error)
}

// isFinite reports whether f is a real number. NaN and the infinities are
// rejected everywhere they can enter: they have no canonical serialization,
// they poison every metric they touch, and they defeat range checks silently
// because every comparison against NaN is false.
func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// IsFinite reports whether f is a real number.
func IsFinite(f float64) bool { return isFinite(f) }

// MarginFromDistribution returns the gap between the largest and second
// largest probability, which is the InstinctMargin for choice and score. A
// distribution with a single entry has no runner-up, so the runner-up is
// taken as zero.
func MarginFromDistribution(dist map[string]float64) (float64, bool) {
	if len(dist) == 0 {
		return 0, false
	}
	vals := make([]float64, 0, len(dist))
	for _, v := range dist {
		if !isFinite(v) {
			return 0, false
		}
		vals = append(vals, v)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(vals)))
	if len(vals) == 1 {
		return vals[0], true
	}
	return vals[0] - vals[1], true
}

// BoolFromProbabilityYes reduces a noul's probability to the answer.
//
// This is argmax over the two classes, not a policy threshold: the answer is
// simply whichever of P(yes) and P(no) is larger. It is written that way
// rather than as a comparison against 0.5 so that it cannot be mistaken for,
// or quietly repurposed as, a confidence cutoff.
//
// An exact tie resolves to false, because a coin flip is not an affirmative
// answer. A caller who cares about the difference between a decisive "no" and
// a tie should read InstinctMargin, which is 0 for the tie.
func BoolFromProbabilityYes(pYes float64) bool {
	pNo := 1 - pYes
	return pYes > pNo
}

// MarginFromProbabilityYes returns the InstinctMargin for a noul.
func MarginFromProbabilityYes(p float64) (float64, bool) {
	if !isFinite(p) {
		return 0, false
	}
	return math.Abs(2*p - 1), true
}

// ApplyEscalation evaluates the caller's rule against the judgment and sets
// Outcome, NeedsEscalation, and EscalationReason accordingly.
//
// With no rule, a successful judgment is ACCEPTABLE_CONFIDENCE and is never
// escalated: absent a caller threshold HowlInstinct has no basis for calling
// anything insufficient, and inventing one here is exactly the failure this
// codebase exists to avoid.
func (j *Judgment) ApplyEscalation(rule EscalationRule) {
	if j.Outcome == OutcomeUnavailable || j.Outcome == OutcomeError {
		return
	}
	j.Outcome = OutcomeAcceptableConfidence
	j.NeedsEscalation = false
	j.EscalationReason = ""

	if rule.IsZero() {
		return
	}

	if rule.MinProviderConfidence != nil {
		if j.ProviderConfidence == nil {
			j.Outcome = OutcomeLowConfidence
			j.NeedsEscalation = true
			j.EscalationReason = ReasonConfidenceUnavailable
			return
		}
		if *j.ProviderConfidence < *rule.MinProviderConfidence {
			j.Outcome = OutcomeLowConfidence
			j.NeedsEscalation = true
			j.EscalationReason = ReasonBelowCallerThreshold
			return
		}
	}

	if rule.MinInstinctMargin != nil {
		if j.InstinctMargin == nil {
			j.Outcome = OutcomeLowConfidence
			j.NeedsEscalation = true
			j.EscalationReason = ReasonConfidenceUnavailable
			return
		}
		if *j.InstinctMargin < *rule.MinInstinctMargin {
			j.Outcome = OutcomeLowConfidence
			j.NeedsEscalation = true
			j.EscalationReason = ReasonBelowCallerThreshold
			return
		}
	}
}

// Float returns a pointer to a copy of f, for building optional fields.
func Float(f float64) *float64 { return &f }

// Bool returns a pointer to a copy of b.
func Bool(b bool) *bool { return &b }

// String returns a pointer to a copy of s.
func String(s string) *string { return &s }

// Int64 returns a pointer to a copy of i.
func Int64(i int64) *int64 { return &i }
