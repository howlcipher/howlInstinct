package eval

import (
	"bytes"
	"strings"
	"testing"
)

func sampleReport() Report {
	acc := 0.75
	brier := 0.41
	return Report{
		Schema:   ReportSchema,
		Dataset:  DatasetInfo{Name: "routing", Path: "evals/routing/cases.yaml", Version: 1, SHA256: "sha256:abc", Cases: 4},
		Provider: "mock", Model: "mock-lexical-baseline-v1",
		StartedAt: "2026-09-22T04:00:45Z", DurationMS: 3,
		Metrics: Metrics{
			Cases: 4, Scored: 4,
			Accuracy: &acc,
			Brier:    &brier,
			Confusion: map[string]map[string]int{
				"bug":     {"bug": 2, "feature": 1},
				"feature": {"feature": 1},
			},
			Latency: LatencyStats{Count: 4, P50MS: 10, P95MS: 20, MaxMS: 20},
		},
		Caveat: "These numbers describe one provider against one dataset at one moment.",
	}
}

func render(r Report) string {
	var b bytes.Buffer
	r.WriteHuman(&b)
	return b.String()
}

// The point of the harness is that an uncomputable metric is visibly
// uncomputable. Printing a number would be a lie; omitting the line entirely
// would invite the reader to assume the metric was fine.
func TestUnavailableMetricsPrintTheirReasonRatherThanANumber(t *testing.T) {
	r := sampleReport()
	r.Metrics.Brier = nil
	r.Metrics.BrierUnavailable = "only 2 of 4 scored cases returned a probability distribution"
	r.Metrics.LogLoss = nil
	r.Metrics.LogLossUnavailable = "only 2 of 4 scored cases returned a probability distribution"

	out := render(r)
	if !strings.Contains(out, "unavailable: only 2 of 4") {
		t.Fatalf("the reason was not printed:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "brier") && strings.Contains(line, "0.0000") {
			t.Fatalf("an unavailable metric was rendered as a number: %q", line)
		}
	}
}

// Zero escalation means "nothing was asked for", not "nothing was uncertain",
// and a reader must be able to tell which.
func TestEscalationRateDistinguishesNoRuleFromNoEscalations(t *testing.T) {
	t.Run("no caller rule", func(t *testing.T) {
		r := sampleReport()
		r.EscalationRuleSupplied = false
		out := render(r)
		if !strings.Contains(out, "no caller threshold was supplied") {
			t.Fatalf("output does not explain why the rate is zero:\n%s", out)
		}
	})

	t.Run("a rule was supplied", func(t *testing.T) {
		r := sampleReport()
		r.EscalationRuleSupplied = true
		r.Metrics.EscalationRate = 0.25
		out := render(r)
		if strings.Contains(out, "no caller threshold") {
			t.Fatalf("output claims no rule was supplied when one was:\n%s", out)
		}
		if !strings.Contains(out, "0.2500") {
			t.Fatalf("the measured rate was not printed:\n%s", out)
		}
	})
}

// The caveat travels with the numbers because they get quoted away from them.
func TestCaveatIsAlwaysRendered(t *testing.T) {
	out := render(sampleReport())
	if !strings.Contains(out, "one provider against one dataset") {
		t.Fatalf("the caveat was not rendered:\n%s", out)
	}
}

// A report must identify what it measured, or it is not evidence of anything.
func TestReportIdentifiesWhatItMeasured(t *testing.T) {
	out := render(sampleReport())
	for _, want := range []string{
		"routing", "evals/routing/cases.yaml", "sha256:abc", "mock",
		"mock-lexical-baseline-v1", "2026-09-22T04:00:45Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report omits %q:\n%s", want, out)
		}
	}
}

// Rendering must not depend on Go's randomized map iteration order, or two
// runs of the same evaluation produce diffs that mean nothing.
func TestRenderingIsDeterministic(t *testing.T) {
	r := sampleReport()
	first := render(r)
	for i := 0; i < 50; i++ {
		if got := render(r); got != first {
			t.Fatalf("report rendering varied between runs")
		}
	}
	if !strings.Contains(first, "bug") || !strings.Contains(first, "feature") {
		t.Fatalf("confusion matrix was not rendered:\n%s", first)
	}
}

func TestFailuresAreRenderedWithTheirReasons(t *testing.T) {
	r := sampleReport()
	r.Metrics.Errored = 1
	r.Failures = []Failure{{CaseID: "route-007", Reason: "provider returned no judgment"}}

	out := render(r)
	if !strings.Contains(out, "route-007") || !strings.Contains(out, "no judgment") {
		t.Fatalf("failures were not rendered with their reasons:\n%s", out)
	}
	if !strings.Contains(out, "errored") {
		t.Fatalf("the errored count was not rendered:\n%s", out)
	}
}

func TestCalibrationRendersWhenPresentAndExplainsItselfWhenNot(t *testing.T) {
	mean, acc := 0.9, 0.85

	withBuckets := sampleReport()
	withBuckets.Metrics.Calibration = []Bucket{
		{LowerBound: 0.8, UpperBound: 0.9, Count: 20, MeanScore: &mean, Accuracy: &acc},
	}
	if out := render(withBuckets); !strings.Contains(out, "calibration") || !strings.Contains(out, "n=20") {
		t.Fatalf("calibration buckets were not rendered:\n%s", out)
	}

	without := sampleReport()
	without.Metrics.CalibrationUnavailable = "no provider returned a probability distribution"
	out := render(without)
	if !strings.Contains(out, "calibration") || !strings.Contains(out, "unavailable") {
		t.Fatalf("missing calibration was not explained:\n%s", out)
	}
}

func TestWrapDoesNotLoseOrDuplicateWords(t *testing.T) {
	const text = "one two three four five six seven eight nine ten eleven twelve"
	got := wrap(text, 20)
	if strings.Join(strings.Fields(got), " ") != text {
		t.Fatalf("wrap changed the text: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 25 {
			t.Errorf("line exceeds the wrap width by more than one word: %q", line)
		}
	}
}
