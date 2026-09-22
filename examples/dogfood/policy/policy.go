// Package policy is a MOCK POLICY CONSUMER. It is not part of HowlInstinct.
//
// It exists to make one boundary impossible to miss. Every threshold, every
// branch, and every decision about what should actually happen lives HERE, in
// the caller, and none of it lives in HowlInstinct. HowlInstinct produced the
// judgments this package reads; it has no idea what this package will do with
// them, and it must not.
//
// In the real Howl ecosystem this role belongs to HowlFrame, which holds
// authority and applies policy to decision receipts. This package is a stand
// in for that, deliberately small enough to read in one sitting.
//
// The rule that matters: nothing below can be moved up into HowlInstinct
// without breaking the architecture. If you find yourself wanting
// HowlInstinct to "just decide" one of these, that is the thing this
// separation exists to prevent.
package policy

import (
	"fmt"

	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

// Thresholds belong to the CALLER. HowlInstinct ships no defaults, has no
// opinion about what counts as confident enough, and cannot see these values.
//
// They are the policy author's judgement about their own risk appetite: how
// wrong they can afford to be, how expensive a false page is against a missed
// incident, and how much human attention they have to spend.
const (
	// confidentEnoughToAct is how decisive a classification must be before
	// this policy will act on it unreviewed.
	confidentEnoughToAct = 0.45

	// severeEnoughToPage is the point on the caller's own severity scale
	// above which a human gets woken up.
	severeEnoughToPage = 3.0
)

// Action is what the CALLER decided to do. Note that HowlInstinct has no
// vocabulary for any of these: it cannot page anyone, file anything, or
// approve anything.
type Action string

const (
	// ActionPageOnCall wakes a human immediately.
	ActionPageOnCall Action = "PAGE_ONCALL"

	// ActionFileTicket records the event for normal working hours.
	ActionFileTicket Action = "FILE_TICKET"

	// ActionEscalateHuman asks a person to look before anything is done. It
	// is this policy's answer whenever the judgments were not decisive
	// enough, or were not available at all.
	ActionEscalateHuman Action = "ESCALATE_TO_HUMAN"

	// ActionIgnore takes no action.
	ActionIgnore Action = "IGNORE"
)

// Decision is the caller's conclusion, with its reasoning.
type Decision struct {
	Action Action
	Why    string
}

// Apply is the deterministic policy: given judgments, decide what happens.
//
// Every branch here is ordinary code. There is no model in this function, no
// probability is consulted except as a number the caller chose to threshold,
// and the same inputs always produce the same action.
func Apply(receipts []receipt.DecisionReceipt) Decision {
	byQuestion := map[string]receipt.DecisionReceipt{}
	for _, r := range receipts {
		byQuestion[r.QuestionID] = r
	}

	incident, hasIncident := byQuestion["is_incident"]
	category, hasCategory := byQuestion["category"]
	severity, hasSeverity := byQuestion["severity"]

	// A judgment that could not be made is not a judgment of "no". The
	// caller decides what to do about missing information, and this caller
	// chooses to involve a human rather than to guess.
	if !hasIncident || !hasCategory || !hasSeverity {
		return Decision{ActionEscalateHuman, "one or more judgments were unavailable"}
	}
	for _, r := range []receipt.DecisionReceipt{incident, category, severity} {
		if r.Outcome == "UNAVAILABLE" || r.Outcome == "ERROR" {
			return Decision{ActionEscalateHuman,
				fmt.Sprintf("judgment %q came back %s", r.QuestionID, r.Outcome)}
		}
	}

	// HowlInstinct reported whether the caller's own escalation rule was
	// met. Acting on that report is still the caller's choice.
	if category.NeedsEscalation {
		return Decision{ActionEscalateHuman,
			fmt.Sprintf("category did not meet the threshold this policy set (%s)",
				category.EscalationReason)}
	}

	if incident.Bool == nil || !*incident.Bool {
		return Decision{ActionIgnore, "not judged to be an incident"}
	}

	// The caller applies its own threshold to the caller's own scale.
	if category.InstinctMargin == nil || *category.InstinctMargin < confidentEnoughToAct {
		return Decision{ActionEscalateHuman,
			"the category judgment was not decisive enough for this policy to route unreviewed"}
	}

	if severity.Score != nil && *severity.Score >= severeEnoughToPage {
		return Decision{ActionPageOnCall,
			fmt.Sprintf("judged an incident, categorised decisively, severity %.2f at or above this policy's paging line of %.1f",
				*severity.Score, severeEnoughToPage)}
	}

	return Decision{ActionFileTicket,
		"judged an incident, but below this policy's paging line"}
}
