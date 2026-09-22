package eval

import (
	"math"
	"strings"
	"testing"
)

func perfect(label string, labels ...string) scoredCase {
	dist := map[string]float64{}
	for _, l := range labels {
		dist[l] = 0
	}
	dist[label] = 1
	return scoredCase{
		expected: expectation{Label: label}, predicted: label,
		correct: true, distribution: dist,
	}
}

func TestAccuracyAndConfusion(t *testing.T) {
	cases := []scoredCase{
		{expected: expectation{Label: "bug"}, predicted: "bug", correct: true},
		{expected: expectation{Label: "bug"}, predicted: "feature"},
		{expected: expectation{Label: "feature"}, predicted: "feature", correct: true},
		{expected: expectation{Label: "feature"}, predicted: "feature", correct: true},
	}
	m := computeMetrics(cases, 4, 0, false)

	if m.Accuracy == nil || math.Abs(*m.Accuracy-0.75) > 1e-9 {
		t.Fatalf("accuracy = %v, want 0.75", m.Accuracy)
	}
	if got := m.Confusion["bug"]["feature"]; got != 1 {
		t.Errorf("confusion[bug][feature] = %d, want 1", got)
	}
	if got := m.Confusion["feature"]["feature"]; got != 2 {
		t.Errorf("confusion[feature][feature] = %d, want 2", got)
	}
}

// The guards are the point of this harness. A metric computed from data that
// cannot support it is worse than no metric, because it looks like evidence.
func TestProbabilisticMetricsAreSuppressedWithoutFullDistributions(t *testing.T) {
	tests := []struct {
		name       string
		cases      []scoredCase
		wantReason string
	}{
		{
			name: "no distributions at all",
			cases: []scoredCase{
				{expected: expectation{Label: "a"}, predicted: "a", correct: true},
				{expected: expectation{Label: "b"}, predicted: "b", correct: true},
			},
			wantReason: "no provider returned a probability distribution",
		},
		{
			// Averaging over only the cases that happen to have
			// distributions would describe a different population than the
			// accuracy figure printed beside it.
			name: "only some cases have distributions",
			cases: []scoredCase{
				perfect("a", "a", "b"),
				{expected: expectation{Label: "b"}, predicted: "b", correct: true},
			},
			wantReason: "only 1 of 2 scored cases",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := computeMetrics(tc.cases, len(tc.cases), 0, false)
			if m.Brier != nil {
				t.Errorf("brier = %v, want it suppressed", *m.Brier)
			}
			if m.LogLoss != nil {
				t.Errorf("log loss = %v, want it suppressed", *m.LogLoss)
			}
			if !strings.Contains(string(m.BrierUnavailable), tc.wantReason) {
				t.Errorf("brier reason = %q, want it to contain %q", m.BrierUnavailable, tc.wantReason)
			}
			// Accuracy does not need probabilities, so it must still appear.
			if m.Accuracy == nil {
				t.Error("accuracy was suppressed although it needs no distribution")
			}
		})
	}
}

func TestEmptyRunSuppressesEverything(t *testing.T) {
	m := computeMetrics(nil, 5, 5, false)
	if m.Accuracy != nil {
		t.Errorf("accuracy = %v, want it suppressed when nothing was scored", *m.Accuracy)
	}
	for _, reason := range []Unavailable{
		m.AccuracyUnavailable, m.BrierUnavailable, m.LogLossUnavailable, m.CalibrationUnavailable,
	} {
		if reason == "" {
			t.Error("a suppressed metric carried no reason")
		}
	}
	if m.Errored != 5 {
		t.Errorf("errored = %d, want 5", m.Errored)
	}
}

// Brier here is the multiclass form: summed squared error against the one-hot
// truth. For two classes its range is 0 to 2, not 0 to 1, because both class
// terms contribute. Pinning the endpoints prevents a silent redefinition.
func TestBrierScoreEndpoints(t *testing.T) {
	t.Run("perfect prediction scores zero", func(t *testing.T) {
		b, ll := probabilisticScores([]scoredCase{perfect("a", "a", "b")})
		if b > 1e-12 {
			t.Errorf("brier = %v, want 0 for a perfect prediction", b)
		}
		if ll > 1e-9 {
			t.Errorf("log loss = %v, want ~0 for a perfect prediction", ll)
		}
	})

	t.Run("confidently wrong scores two", func(t *testing.T) {
		c := scoredCase{
			expected:     expectation{Label: "a"},
			predicted:    "b",
			distribution: map[string]float64{"a": 0, "b": 1},
		}
		b, _ := probabilisticScores([]scoredCase{c})
		if math.Abs(b-2.0) > 1e-9 {
			t.Errorf("brier = %v, want 2 for a confidently wrong two-class prediction", b)
		}
	})

	t.Run("maximum uncertainty scores one half", func(t *testing.T) {
		c := scoredCase{
			expected:     expectation{Label: "a"},
			predicted:    "a",
			correct:      true,
			distribution: map[string]float64{"a": 0.5, "b": 0.5},
		}
		b, _ := probabilisticScores([]scoredCase{c})
		if math.Abs(b-0.5) > 1e-9 {
			t.Errorf("brier = %v, want 0.5 for an even two-class split", b)
		}
	})
}

// A single confident mistake must not make the whole run's log loss infinite,
// which would destroy the information carried by every other case.
func TestLogLossIsClampedAgainstZeroProbability(t *testing.T) {
	cases := []scoredCase{
		perfect("a", "a", "b"),
		{
			expected:     expectation{Label: "a"},
			predicted:    "b",
			distribution: map[string]float64{"a": 0, "b": 1},
		},
	}
	_, ll := probabilisticScores(cases)
	if math.IsInf(ll, 0) || math.IsNaN(ll) {
		t.Fatalf("log loss = %v, want a finite clamped value", ll)
	}
	if ll <= 0 {
		t.Fatalf("log loss = %v, want a positive penalty for the confident mistake", ll)
	}
}

func TestCalibrationBinsAndSkipsEmptyBuckets(t *testing.T) {
	var cases []scoredCase
	// Ten predictions at 0.9 confidence, nine of them correct: a
	// well-calibrated bin.
	for i := 0; i < 10; i++ {
		c := scoredCase{
			expected:     expectation{Label: "a"},
			predicted:    "a",
			correct:      i < 9,
			distribution: map[string]float64{"a": 0.9, "b": 0.1},
		}
		if !c.correct {
			c.expected = expectation{Label: "b"}
		}
		cases = append(cases, c)
	}

	buckets := calibration(cases)
	if len(buckets) != 1 {
		t.Fatalf("got %d buckets, want only the populated one", len(buckets))
	}
	b := buckets[0]
	if b.Count != 10 {
		t.Errorf("bucket count = %d, want 10", b.Count)
	}
	if b.Accuracy == nil || math.Abs(*b.Accuracy-0.9) > 1e-9 {
		t.Errorf("observed accuracy = %v, want 0.9", b.Accuracy)
	}
	if b.MeanScore == nil || math.Abs(*b.MeanScore-0.9) > 1e-9 {
		t.Errorf("mean predicted = %v, want 0.9", b.MeanScore)
	}
}

// Mean absolute error is what makes an ordinal scale measurable: predicting
// "high" when the truth is "critical" is a smaller mistake than predicting
// "negligible", and accuracy cannot see the difference.
func TestMeanAbsoluteErrorMeasuresOrdinalDistance(t *testing.T) {
	near := []scoredCase{{expected: expectation{Index: 4}, predictedScore: f(3)}}
	far := []scoredCase{{expected: expectation{Index: 4}, predictedScore: f(0)}}

	nearMAE, ok := meanAbsoluteError(near)
	if !ok || math.Abs(nearMAE-1) > 1e-9 {
		t.Fatalf("near MAE = %v (ok=%v), want 1", nearMAE, ok)
	}
	farMAE, ok := meanAbsoluteError(far)
	if !ok || math.Abs(farMAE-4) > 1e-9 {
		t.Fatalf("far MAE = %v (ok=%v), want 4", farMAE, ok)
	}
	if nearMAE >= farMAE {
		t.Fatal("a near miss did not score better than a far one")
	}
}

func TestMeanAbsoluteErrorIsSuppressedForNonScoreQuestions(t *testing.T) {
	m := computeMetrics([]scoredCase{perfect("a", "a", "b")}, 1, 0, false)
	if m.MAE != nil {
		t.Fatalf("MAE = %v, want it suppressed for a non-score question", *m.MAE)
	}
	if !strings.Contains(string(m.MAEUnavailable), "score questions") {
		t.Fatalf("MAE reason = %q, want it to explain the suppression", m.MAEUnavailable)
	}
}

// Nearest-rank means every reported percentile is a latency that actually
// occurred, rather than an interpolated value nobody observed.
func TestLatencyPercentiles(t *testing.T) {
	stats := latencyStats([]int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100})
	if stats.Count != 10 {
		t.Errorf("count = %d, want 10", stats.Count)
	}
	if stats.P50MS != 50 {
		t.Errorf("p50 = %d, want 50", stats.P50MS)
	}
	if stats.P95MS != 100 {
		t.Errorf("p95 = %d, want 100", stats.P95MS)
	}
	if stats.MaxMS != 100 {
		t.Errorf("max = %d, want 100", stats.MaxMS)
	}

	if got := latencyStats(nil); got.Count != 0 {
		t.Errorf("empty latency stats = %+v, want zero", got)
	}
	if got := latencyStats([]int64{7}); got.P50MS != 7 || got.P95MS != 7 {
		t.Errorf("single-sample stats = %+v, want 7 everywhere", got)
	}
}

// With no caller rule there is nothing to escalate against, so the rate must
// be zero rather than something inferred from the judgments.
func TestEscalationRateReflectsOnlyMarkedJudgments(t *testing.T) {
	cases := []scoredCase{
		{expected: expectation{Label: "a"}, predicted: "a", correct: true},
		{expected: expectation{Label: "a"}, predicted: "a", correct: true, escalated: true},
	}
	m := computeMetrics(cases, 2, 0, false)
	if math.Abs(m.EscalationRate-0.5) > 1e-9 {
		t.Fatalf("escalation rate = %v, want 0.5", m.EscalationRate)
	}
}

func f(v float64) *float64 { return &v }
