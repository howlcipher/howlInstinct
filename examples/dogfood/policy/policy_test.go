package policy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

func base(id string) receipt.DecisionReceipt {
	return receipt.DecisionReceipt{
		QuestionID: id,
		Outcome:    instinct.OutcomeAcceptableConfidence,
	}
}

func incidentSet(isIncident bool, margin, severity float64, escalate bool) []receipt.DecisionReceipt {
	inc := base("is_incident")
	inc.Bool = instinct.Bool(isIncident)

	cat := base("category")
	cat.Choice = instinct.String("outage")
	cat.InstinctMargin = instinct.Float(margin)
	if escalate {
		cat.NeedsEscalation = true
		cat.EscalationReason = instinct.ReasonBelowCallerThreshold
		cat.Outcome = instinct.OutcomeLowConfidence
	}

	sev := base("severity")
	sev.Score = instinct.Float(severity)

	return []receipt.DecisionReceipt{inc, cat, sev}
}

func TestPolicyBranches(t *testing.T) {
	tests := []struct {
		name     string
		receipts []receipt.DecisionReceipt
		want     Action
	}{
		{
			"decisive severe incident pages someone",
			incidentSet(true, 0.8, 4.0, false),
			ActionPageOnCall,
		},
		{
			"decisive mild incident files a ticket",
			incidentSet(true, 0.8, 1.0, false),
			ActionFileTicket,
		},
		{
			"not an incident is ignored",
			incidentSet(false, 0.8, 4.0, false),
			ActionIgnore,
		},
		{
			"an indecisive category goes to a human",
			incidentSet(true, 0.1, 4.0, false),
			ActionEscalateHuman,
		},
		{
			"a judgment the caller's own rule flagged goes to a human",
			incidentSet(true, 0.8, 4.0, true),
			ActionEscalateHuman,
		},
		{
			"missing judgments go to a human, not to a default",
			[]receipt.DecisionReceipt{base("is_incident")},
			ActionEscalateHuman,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Apply(tc.receipts)
			if got.Action != tc.want {
				t.Fatalf("Apply() = %s (%s), want %s", got.Action, got.Why, tc.want)
			}
			if got.Why == "" {
				t.Error("Apply() gave no reason for its action")
			}
		})
	}
}

// An unavailable judgment is not a judgment of "no". A policy that treated it
// as one would silently downgrade every provider outage into "nothing is
// wrong", which is the most dangerous possible failure mode.
func TestUnavailableJudgmentIsNotTreatedAsNegative(t *testing.T) {
	for _, outcome := range []instinct.Outcome{
		instinct.OutcomeUnavailable, instinct.OutcomeError,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			receipts := incidentSet(true, 0.9, 4.0, false)
			receipts[0].Outcome = outcome
			receipts[0].Bool = nil

			if got := Apply(receipts); got.Action != ActionEscalateHuman {
				t.Fatalf("Apply() = %s, want %s when a judgment is %s",
					got.Action, ActionEscalateHuman, outcome)
			}
		})
	}
}

func TestPolicyIsDeterministic(t *testing.T) {
	receipts := incidentSet(true, 0.8, 4.0, false)
	first := Apply(receipts)
	for i := 0; i < 100; i++ {
		if got := Apply(receipts); got != first {
			t.Fatalf("policy varied between runs: %+v then %+v", first, got)
		}
	}
}

// The architectural assertion this example exists to make: every threshold
// lives in the caller. This walks the HowlInstinct packages and fails if a
// decision threshold has drifted into them.
//
// It is a blunt instrument by design. A float literal being compared against
// a judgment's confidence, probability, or margin inside HowlInstinct is
// exactly the shape of "confidence became permission", and it should be
// impossible to add one without someone noticing.
//
// What this does NOT forbid is argmax: choosing the larger of two class
// probabilities is how a distribution becomes an answer, and it is not a
// threshold. That case is written as a comparison between the two
// probabilities (see instinct.BoolFromProbabilityYes) rather than against a
// constant, which keeps the distinction visible in the source instead of
// resting on an exemption list here.
//
// It also does not, and cannot, catch a threshold hidden behind a named
// constant or arriving from configuration. It is one guard, not a proof.
func TestNoDecisionThresholdLivesInsideHowlInstinct(t *testing.T) {
	roots := []string{
		filepath.Join("..", "..", "..", "pkg", "instinct"),
		filepath.Join("..", "..", "..", "internal", "decision"),
		filepath.Join("..", "..", "..", "internal", "provider", "jev"),
	}

	// Fields whose value is a judgment's uncertainty. Comparing one of these
	// against a literal inside HowlInstinct is the thing being forbidden.
	judgmentFields := map[string]bool{
		"ProviderConfidence": true,
		"InstinctMargin":     true,
		"ProbabilityYes":     true,
		"Confidence":         true,
		"Noul":               true,
	}

	for _, root := range roots {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, root, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", root, err)
		}
		for _, pkg := range pkgs {
			for name, file := range pkg.Files {
				if strings.HasSuffix(name, "_test.go") {
					continue
				}
				ast.Inspect(file, func(n ast.Node) bool {
					cmp, ok := n.(*ast.BinaryExpr)
					if !ok {
						return true
					}
					switch cmp.Op {
					case token.LSS, token.GTR, token.LEQ, token.GEQ:
					default:
						return true
					}
					if touchesJudgmentField(cmp.X, judgmentFields) && isNumericLiteral(cmp.Y) ||
						touchesJudgmentField(cmp.Y, judgmentFields) && isNumericLiteral(cmp.X) {
						t.Errorf(
							"%s: a judgment's uncertainty is compared against a hardcoded threshold "+
								"inside HowlInstinct. Thresholds belong to the caller.",
							fset.Position(cmp.Pos()))
					}
					return true
				})
			}
		}
	}
}

func touchesJudgmentField(e ast.Expr, fields map[string]bool) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && fields[sel.Sel.Name] {
			found = true
		}
		return !found
	})
	return found
}

func isNumericLiteral(e ast.Expr) bool {
	if unary, ok := e.(*ast.UnaryExpr); ok {
		e = unary.X
	}
	lit, ok := e.(*ast.BasicLit)
	return ok && (lit.Kind == token.FLOAT || lit.Kind == token.INT)
}
