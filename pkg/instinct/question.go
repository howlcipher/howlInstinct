package instinct

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// Option is one selectable value of a choice question. Description is
// optional context for the provider; Name is what comes back as the answer.
type Option struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Level is one rung of a score scale. Levels are ordered, and a level's
// position in the slice is its numeric value: the first level is 0.
type Level struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Question is a single bounded question about the request's state.
//
// Only the fields meaningful for the question's type may be populated.
// Cross-field validation is strict rather than lenient: a choice question
// carrying score levels is a caller bug, and silently ignoring the levels
// would hide it until the answers looked wrong.
type Question struct {
	Type         DecisionType `json:"type"`
	Instructions string       `json:"instructions"`

	// TrueMeaning and FalseMeaning optionally describe what yes and no mean
	// for a noul question.
	TrueMeaning  string `json:"true_meaning,omitempty"`
	FalseMeaning string `json:"false_meaning,omitempty"`

	// Options are the selectable values of a choice or classify question.
	Options []Option `json:"options,omitempty"`

	// Levels are the ordered rungs of a score question.
	Levels []Level `json:"levels,omitempty"`
}

// questionIDPattern constrains caller-supplied question identifiers.
//
// IDs are echoed into receipts, used as map keys, compared against provider
// responses, and printed in logs. Constraining them to an obvious, printable,
// shell-safe shape keeps all of those uses unambiguous and keeps control
// characters out of log lines.
var questionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidateID checks a single question identifier.
func ValidateID(id string, lim Limits) error {
	if id == "" {
		return Errorf(KindInvalidInput, "validate", "question id is empty")
	}
	if len(id) > lim.MaxIDBytes {
		return Errorf(KindInvalidInput, "validate",
			"question id %q is %d bytes, limit is %d", id, len(id), lim.MaxIDBytes)
	}
	if !questionIDPattern.MatchString(id) {
		return Errorf(KindInvalidInput, "validate",
			"question id %q must start alphanumeric and contain only letters, digits, underscore, dot, or hyphen", id)
	}
	return nil
}

// Validate checks a question against the supplied limits.
func (q Question) Validate(id string, lim Limits) error {
	if !q.Type.IsValid() {
		return Errorf(KindInvalidInput, "validate",
			"question %q has unknown type %q", id, q.Type)
	}
	if q.Instructions == "" {
		return Errorf(KindInvalidInput, "validate",
			"question %q has empty instructions", id)
	}
	if !utf8.ValidString(q.Instructions) {
		return Errorf(KindInvalidInput, "validate",
			"question %q instructions are not valid UTF-8", id)
	}
	if len(q.Instructions) > lim.MaxInstructionBytes {
		return Errorf(KindInvalidInput, "validate",
			"question %q instructions are %d bytes, limit is %d",
			id, len(q.Instructions), lim.MaxInstructionBytes)
	}

	switch q.Type.Lower() {
	case TypeNoul:
		return q.validateNoul(id, lim)
	case TypeChoice:
		return q.validateChoice(id, lim)
	case TypeScore:
		return q.validateScore(id, lim)
	default:
		return Errorf(KindInvalidInput, "validate",
			"question %q has unhandled type %q", id, q.Type)
	}
}

func (q Question) validateNoul(id string, lim Limits) error {
	if len(q.Options) > 0 {
		return Errorf(KindInvalidInput, "validate",
			"question %q is noul but supplies choice options", id)
	}
	if len(q.Levels) > 0 {
		return Errorf(KindInvalidInput, "validate",
			"question %q is noul but supplies score levels", id)
	}
	for _, f := range []struct{ what, val string }{
		{"true_meaning", q.TrueMeaning},
		{"false_meaning", q.FalseMeaning},
	} {
		if len(f.val) > lim.MaxLabelBytes {
			return Errorf(KindInvalidInput, "validate",
				"question %q %s is %d bytes, limit is %d",
				id, f.what, len(f.val), lim.MaxLabelBytes)
		}
	}
	return nil
}

func (q Question) validateChoice(id string, lim Limits) error {
	if len(q.Levels) > 0 {
		return Errorf(KindInvalidInput, "validate",
			"question %q is %s but supplies score levels", id, q.Type)
	}
	if len(q.Options) < 1 {
		return Errorf(KindInvalidInput, "validate",
			"question %q is %s but supplies no options", id, q.Type)
	}
	if len(q.Options) > lim.MaxOptions {
		return Errorf(KindInvalidInput, "validate",
			"question %q has %d options, limit is %d", id, len(q.Options), lim.MaxOptions)
	}
	seen := make(map[string]bool, len(q.Options))
	for i, o := range q.Options {
		if err := validateLabel(id, fmt.Sprintf("option %d", i), o.Name, o.Description, lim); err != nil {
			return err
		}
		if seen[o.Name] {
			return Errorf(KindInvalidInput, "validate",
				"question %q repeats option name %q", id, o.Name)
		}
		seen[o.Name] = true
	}
	return nil
}

func (q Question) validateScore(id string, lim Limits) error {
	if len(q.Options) > 0 {
		return Errorf(KindInvalidInput, "validate",
			"question %q is score but supplies choice options", id)
	}
	if len(q.Levels) < lim.MinLevels || len(q.Levels) > lim.MaxLevels {
		return Errorf(KindInvalidInput, "validate",
			"question %q has %d score levels, allowed range is %d to %d",
			id, len(q.Levels), lim.MinLevels, lim.MaxLevels)
	}
	seen := make(map[string]bool, len(q.Levels))
	for i, l := range q.Levels {
		if err := validateLabel(id, fmt.Sprintf("level %d", i), l.Name, l.Description, lim); err != nil {
			return err
		}
		// Level names double as distribution keys, so a repeat would make the
		// returned probabilities ambiguous rather than merely untidy.
		if seen[l.Name] {
			return Errorf(KindInvalidInput, "validate",
				"question %q repeats level name %q", id, l.Name)
		}
		seen[l.Name] = true
	}
	return nil
}

func validateLabel(id, what, name, desc string, lim Limits) error {
	if name == "" {
		return Errorf(KindInvalidInput, "validate", "question %q %s has an empty name", id, what)
	}
	if !utf8.ValidString(name) || !utf8.ValidString(desc) {
		return Errorf(KindInvalidInput, "validate", "question %q %s is not valid UTF-8", id, what)
	}
	if len(name) > lim.MaxLabelBytes {
		return Errorf(KindInvalidInput, "validate",
			"question %q %s name is %d bytes, limit is %d", id, what, len(name), lim.MaxLabelBytes)
	}
	if len(desc) > lim.MaxLabelBytes {
		return Errorf(KindInvalidInput, "validate",
			"question %q %s description is %d bytes, limit is %d", id, what, len(desc), lim.MaxLabelBytes)
	}
	return nil
}

// LevelNames returns the ordered level names of a score question. The result
// is the legend: index i is the score value i.
func (q Question) LevelNames() []string {
	out := make([]string, len(q.Levels))
	for i, l := range q.Levels {
		out[i] = l.Name
	}
	return out
}

// OptionNames returns the option names of a choice or classify question.
func (q Question) OptionNames() []string {
	out := make([]string, len(q.Options))
	for i, o := range q.Options {
		out[i] = o.Name
	}
	return out
}
