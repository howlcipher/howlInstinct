// Command dogfood demonstrates the whole HowlInstinct loop end to end, and
// makes the authority boundary visible.
//
//	incoming engineering event
//	         |
//	         v
//	   HowlInstinct          <- judges. Knows nothing about consequences.
//	   ├─ is_incident  noul
//	   ├─ category     choice
//	   └─ severity     score
//	         |
//	         v
//	   Decision Receipts     <- durable, hashed, credential free
//	         |
//	         v
//	   mock policy consumer  <- decides. Owns every threshold and every action.
//	         |
//	         ├─ deterministic branch
//	         └─ escalate
//
// Everything above the receipts is HowlInstinct. Everything below is the
// caller. The two halves are in different packages, and the lower half is not
// importable from the upper one, which is the point.
//
// Run it with: go run ./examples/dogfood
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/howlcipher/howlinstinct/examples/dogfood/policy"
	"github.com/howlcipher/howlinstinct/internal/decision"
	"github.com/howlcipher/howlinstinct/internal/provider/mock"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

// event is one thing that happened, as it might arrive from an alerting
// system, a ticket queue, or a chat message.
type event struct {
	name  string
	state string
}

var events = []event{
	{
		name: "clear-cut outage",
		state: "All payment requests are returning HTTP 500 across every region. " +
			"Customers cannot check out. The failing requests started 8 minutes ago " +
			"and the error rate is 100 percent.",
	},
	{
		name:  "ambiguous report",
		state: "someone said the thing looked weird earlier",
	},
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dogfood:", err)
		os.Exit(1)
	}
}

func run(out *os.File) error {
	// The mock provider needs no network, no credentials, and no hardware,
	// which is why this example runs anywhere.
	engine := decision.New(mock.New(), decision.WithTimeout(5*time.Second))

	for i, ev := range events {
		if i > 0 {
			fmt.Fprintln(out, strings.Repeat("=", 72))
		}
		if err := handle(out, engine, ev); err != nil {
			return err
		}
	}
	return nil
}

func handle(out *os.File, engine *decision.Engine, ev event) error {
	fmt.Fprintf(out, "EVENT: %s\n", ev.name)
	fmt.Fprintf(out, "  %s\n\n", wrapText(ev.state, 68, "  "))

	req := buildRequest(ev.state)

	res, err := engine.Decide(context.Background(), req)
	if err != nil {
		// Not fatal to the demo: a failed decision is a case the policy
		// consumer has to handle, and showing that is the point.
		fmt.Fprintf(out, "  decision failed: %v\n\n", err)
	}

	fmt.Fprintln(out, "HOWLINSTINCT judges (it does not act):")
	for _, id := range req.QuestionIDs() {
		printJudgment(out, id, res.Response.Judgments[id])
	}

	fmt.Fprintf(out, "\nDECISION RECEIPTS: %d, batch %s\n", len(res.Receipts), res.BatchID)
	if len(res.Receipts) > 0 {
		r := res.Receipts[0]
		fmt.Fprintf(out, "  state is recorded as a hash, not as text: %s\n", r.StateHash)
		fmt.Fprintf(out, "  question hash: %s\n", r.QuestionHash)
	}

	// ------------------------------------------------------------------
	// Everything from here is the CALLER. HowlInstinct is done.
	// ------------------------------------------------------------------
	verdict := policy.Apply(res.Receipts)

	fmt.Fprintln(out, "\nMOCK POLICY CONSUMER decides (HowlInstinct cannot):")
	fmt.Fprintf(out, "  action: %s\n", verdict.Action)
	fmt.Fprintf(out, "  because: %s\n\n", verdict.Why)
	return nil
}

func buildRequest(state string) instinct.Request {
	return instinct.Request{
		State: state,
		Questions: map[string]instinct.Question{
			"is_incident": {
				Type:         instinct.TypeNoul,
				Instructions: "Is this an active production incident affecting customers?",
				TrueMeaning:  "production is broken and customers are affected right now",
				FalseMeaning: "routine, cosmetic, or unclear, with no customer impact",
			},
			"category": {
				Type:         instinct.TypeChoice,
				Instructions: "Which category does this belong to?",
				Options: []instinct.Option{
					{Name: "outage", Description: "requests failing errors unavailable regions customers"},
					{Name: "billing", Description: "invoices charges refunds subscription pricing"},
					{Name: "performance", Description: "slow latency timeouts degraded throughput"},
					{Name: "unknown", Description: "not enough information to tell"},
				},
			},
			"severity": {
				Type:         instinct.TypeScore,
				Instructions: "How severe is this?",
				Levels: []instinct.Level{
					{Name: "negligible", Description: "nobody notices"},
					{Name: "low", Description: "minor inconvenience for a few"},
					{Name: "moderate", Description: "a subset of customers degraded"},
					{Name: "high", Description: "many customers affected checkout payment"},
					{Name: "critical", Description: "all customers every region cannot complete requests"},
				},
			},
		},
		// The threshold below is the CALLER's, supplied per request.
		// HowlInstinct has no default and would escalate nothing without it.
		Escalation: map[string]instinct.EscalationRule{
			"category": {MinInstinctMargin: instinct.Float(0.15)},
		},
		CorrelationID: "dogfood-demo",
	}
}

func printJudgment(out *os.File, id string, j instinct.Judgment) {
	fmt.Fprintf(out, "  %-12s ", id)
	switch {
	case j.Bool != nil:
		fmt.Fprintf(out, "%-10s P(yes)=%.3f", yesNo(*j.Bool), deref(j.ProbabilityYes))
	case j.Choice != nil:
		fmt.Fprintf(out, "%-10s margin=%.3f", *j.Choice, deref(j.InstinctMargin))
	case j.Score != nil:
		fmt.Fprintf(out, "%-10.3f on %s", *j.Score, strings.Join(j.Legend, " < "))
	default:
		fmt.Fprintf(out, "%-10s", "(none)")
	}

	// Printed as unavailable rather than as a number, because a noul has no
	// provider confidence and showing 0.000 would invent one.
	if j.ProviderConfidence != nil {
		fmt.Fprintf(out, "  provider confidence=%.3f", *j.ProviderConfidence)
	} else {
		fmt.Fprint(out, "  provider confidence=none reported")
	}
	if j.NeedsEscalation {
		fmt.Fprintf(out, "  [escalate: %s]", j.EscalationReason)
	}
	fmt.Fprintln(out)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func wrapText(s string, width int, indent string) string {
	var b strings.Builder
	line := 0
	for i, word := range strings.Fields(s) {
		if line+len(word)+1 > width && i > 0 {
			b.WriteString("\n" + indent)
			line = 0
		} else if i > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(word)
		line += len(word)
	}
	return b.String()
}

// compile-time check that the example only reads receipts and never reaches
// into HowlInstinct's decision machinery to change an outcome.
var _ = func(r []receipt.DecisionReceipt) policy.Decision { return policy.Apply(r) }
