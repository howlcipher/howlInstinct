package instinct

// Outcome is a policy-neutral classification of how a decision turned out.
//
// These values deliberately describe the *state of the judgment*, never what
// the caller should do about it. There is no OUTCOME_APPROVED and no
// OUTCOME_DENIED, because HowlInstinct does not approve or deny anything.
type Outcome string

const (
	// OutcomeAcceptableConfidence means a judgment was produced and, if the
	// caller supplied an escalation rule, it was satisfied. With no caller
	// rule, every successful judgment is acceptable: HowlInstinct holds no
	// opinion of its own about what counts as confident enough.
	OutcomeAcceptableConfidence Outcome = "ACCEPTABLE_CONFIDENCE"

	// OutcomeLowConfidence means a judgment was produced but did not satisfy
	// an escalation rule the caller supplied. It is never set otherwise.
	OutcomeLowConfidence Outcome = "LOW_CONFIDENCE"

	// OutcomeUnavailable means no judgment could be obtained: the provider
	// was unreachable, timed out, or declined.
	OutcomeUnavailable Outcome = "UNAVAILABLE"

	// OutcomeError means a judgment was returned but could not be trusted,
	// typically because it failed response validation.
	OutcomeError Outcome = "ERROR"
)

var validOutcomes = map[Outcome]bool{
	OutcomeAcceptableConfidence: true,
	OutcomeLowConfidence:        true,
	OutcomeUnavailable:          true,
	OutcomeError:                true,
}

// IsValid reports whether o is a known outcome class.
func (o Outcome) IsValid() bool { return validOutcomes[o] }

// String returns the wire spelling of the outcome.
func (o Outcome) String() string { return string(o) }

// Escalation reasons. These are the only reasons HowlInstinct will ever set,
// and both of them are consequences of a rule the caller supplied.
const (
	// ReasonBelowCallerThreshold means the caller's own threshold was not met.
	ReasonBelowCallerThreshold = "BELOW_CALLER_THRESHOLD"

	// ReasonConfidenceUnavailable means the caller asked for a judgment to be
	// gated on provider confidence, but the provider returned none. This is
	// reported rather than papered over: substituting a probability for a
	// missing confidence would be inventing data.
	ReasonConfidenceUnavailable = "CONFIDENCE_UNAVAILABLE"
)
