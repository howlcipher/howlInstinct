package instinct

// DecisionType names one of the bounded question shapes HowlInstinct accepts.
//
// Three of these are provider primitives. Classify is not: it is a
// HowlInstinct-level convenience that is lowered to Choice before any request
// reaches a provider. Adding a fourth provider primitive is explicitly out of
// scope, because a primitive the provider cannot express would have to be
// faked somewhere, and faking it would mean inventing uncertainty.
type DecisionType string

const (
	// TypeNoul is a bounded yes/no judgment. A noul carries a probability of
	// yes and, per the upstream contract, no separate confidence value: the
	// probability is itself the uncertainty signal.
	TypeNoul DecisionType = "noul"

	// TypeChoice selects exactly one value from a finite set of options.
	TypeChoice DecisionType = "choice"

	// TypeScore places state on a bounded ordinal scale whose levels the
	// caller supplies explicitly. Level index is the score value.
	TypeScore DecisionType = "score"

	// TypeClassify is sugar over TypeChoice. See Lower.
	TypeClassify DecisionType = "classify"
)

var validDecisionTypes = map[DecisionType]bool{
	TypeNoul:     true,
	TypeChoice:   true,
	TypeScore:    true,
	TypeClassify: true,
}

// IsValid reports whether t is a decision type HowlInstinct understands.
func (t DecisionType) IsValid() bool { return validDecisionTypes[t] }

// String returns the wire spelling of the decision type.
func (t DecisionType) String() string { return string(t) }

// Lower returns the type a provider will actually be asked for. Classify
// lowers to choice; every other type lowers to itself.
//
// Lowering is recorded on the resulting Judgment (as Type plus CompiledTo) so
// that a receipt still shows what the caller asked for, not merely what the
// provider was asked.
func (t DecisionType) Lower() DecisionType {
	if t == TypeClassify {
		return TypeChoice
	}
	return t
}
