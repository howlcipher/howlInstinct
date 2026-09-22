package eval

import (
	"fmt"
	"math"
	"sort"
)

// Unavailable explains why a metric was not computed.
//
// It exists so that a report can distinguish "this value is zero" from "this
// value is unknown". Emitting 0 for an uncomputable metric is the single
// easiest way for an evaluation harness to lie, and the JSON shape
// deliberately makes the distinction explicit: the metric is null and a
// reason sits beside it.
type Unavailable string

// Metrics is everything measured over one evaluation run.
type Metrics struct {
	Cases   int `json:"cases"`
	Scored  int `json:"scored"`
	Errored int `json:"errored"`

	Accuracy            *float64    `json:"accuracy"`
	AccuracyUnavailable Unavailable `json:"accuracy_unavailable_reason,omitempty"`

	// Confusion is expected label -> predicted label -> count.
	Confusion map[string]map[string]int `json:"confusion,omitempty"`

	// Brier is the multiclass Brier score: the summed squared error between
	// the predicted distribution and the one-hot truth. Lower is better. For
	// a two-class question its range is 0 to 2, not 0 to 1, because both
	// class terms contribute.
	Brier            *float64    `json:"brier"`
	BrierUnavailable Unavailable `json:"brier_unavailable_reason,omitempty"`

	// LogLoss is the mean negative log probability of the true class,
	// clamped away from zero so a single confident mistake cannot render the
	// whole run as infinity.
	LogLoss            *float64    `json:"log_loss"`
	LogLossUnavailable Unavailable `json:"log_loss_unavailable_reason,omitempty"`

	Calibration            []Bucket    `json:"calibration,omitempty"`
	CalibrationUnavailable Unavailable `json:"calibration_unavailable_reason,omitempty"`

	// EscalationRate is the share of judgments the caller's own rule marked
	// for escalation. With no rule supplied it is 0 by construction, which
	// the report states rather than leaving to be inferred.
	EscalationRate float64 `json:"escalation_rate"`

	// MAE and ExactMatch apply to score questions only.
	MAE            *float64    `json:"mean_absolute_error"`
	MAEUnavailable Unavailable `json:"mean_absolute_error_unavailable_reason,omitempty"`

	Latency LatencyStats `json:"latency"`
}

// Bucket is one calibration bin: how often predictions in a confidence range
// turned out to be right.
type Bucket struct {
	LowerBound float64  `json:"lower_bound"`
	UpperBound float64  `json:"upper_bound"`
	Count      int      `json:"count"`
	MeanScore  *float64 `json:"mean_predicted"`
	Accuracy   *float64 `json:"observed_accuracy"`
}

// LatencyStats summarizes call durations.
type LatencyStats struct {
	Count int   `json:"count"`
	P50MS int64 `json:"p50_ms"`
	P95MS int64 `json:"p95_ms"`
	MaxMS int64 `json:"max_ms"`
}

// scoredCase is one measured outcome, reduced to what the metrics need.
type scoredCase struct {
	expected  expectation
	predicted string
	correct   bool

	// distribution over class labels, when the provider supplied one.
	distribution map[string]float64

	// predictedIndex and expectedIndex are populated for score questions.
	predictedScore *float64

	escalated bool
	latencyMS int64
}

// computeMetrics folds scored cases into a report.
//
// Every metric here is guarded. The guards are the substance of this
// function: computing accuracy over zero cases, a Brier score over a run
// where some providers returned no distribution, or a calibration curve from
// three data points would all produce numbers, and every one of them would
// mislead whoever read it.
func computeMetrics(cases []scoredCase, totalCases, errored int, scoreType bool) Metrics {
	m := Metrics{
		Cases:     totalCases,
		Scored:    len(cases),
		Errored:   errored,
		Confusion: map[string]map[string]int{},
	}

	if len(cases) == 0 {
		m.AccuracyUnavailable = "no case produced a judgment"
		m.BrierUnavailable = "no case produced a judgment"
		m.LogLossUnavailable = "no case produced a judgment"
		m.CalibrationUnavailable = "no case produced a judgment"
		m.MAEUnavailable = "no case produced a judgment"
		return m
	}

	var correct, escalated int
	latencies := make([]int64, 0, len(cases))
	for _, c := range cases {
		if c.correct {
			correct++
		}
		if c.escalated {
			escalated++
		}
		latencies = append(latencies, c.latencyMS)
		if m.Confusion[c.expected.Label] == nil {
			m.Confusion[c.expected.Label] = map[string]int{}
		}
		m.Confusion[c.expected.Label][c.predicted]++
	}

	acc := float64(correct) / float64(len(cases))
	m.Accuracy = &acc
	m.EscalationRate = float64(escalated) / float64(len(cases))
	m.Latency = latencyStats(latencies)

	withDist := 0
	for _, c := range cases {
		if len(c.distribution) > 0 {
			withDist++
		}
	}

	switch {
	case withDist == 0:
		reason := Unavailable("no provider returned a probability distribution")
		m.BrierUnavailable, m.LogLossUnavailable, m.CalibrationUnavailable = reason, reason, reason
	case withDist < len(cases):
		// Averaging over the subset that happens to have distributions would
		// silently report a metric for a different population than the one
		// the accuracy figure describes.
		reason := Unavailable(fmt.Sprintf(
			"only %d of %d scored cases returned a probability distribution", withDist, len(cases)))
		m.BrierUnavailable, m.LogLossUnavailable, m.CalibrationUnavailable = reason, reason, reason
	default:
		brier, logloss := probabilisticScores(cases)
		m.Brier, m.LogLoss = &brier, &logloss
		m.Calibration = calibration(cases)
		if len(m.Calibration) == 0 {
			m.CalibrationUnavailable = "no bucket contained a prediction"
		}
	}

	if scoreType {
		mae, ok := meanAbsoluteError(cases)
		if ok {
			m.MAE = &mae
		} else {
			m.MAEUnavailable = "no case returned a numeric score"
		}
	} else {
		m.MAEUnavailable = "mean absolute error applies only to score questions"
	}
	return m
}

// probabilisticScores computes the multiclass Brier score and log loss.
func probabilisticScores(cases []scoredCase) (brier, logloss float64) {
	const (
		// Clamping keeps log loss finite. A provider reporting probability
		// zero for the outcome that actually happened would otherwise make
		// the whole run's log loss infinite, destroying the information in
		// every other case.
		epsilon = 1e-15
	)
	for _, c := range cases {
		var caseBrier float64
		for label, p := range c.distribution {
			target := 0.0
			if label == c.expected.Label {
				target = 1.0
			}
			diff := p - target
			caseBrier += diff * diff
		}
		brier += caseBrier

		pTrue := c.distribution[c.expected.Label]
		pTrue = math.Min(1-epsilon, math.Max(epsilon, pTrue))
		logloss += -math.Log(pTrue)
	}
	n := float64(len(cases))
	return brier / n, logloss / n
}

// calibration bins predictions by the probability assigned to the predicted
// class and reports how often those predictions were right.
//
// A well-calibrated provider that says 0.8 should be right about 80% of the
// time. Empty bins are omitted rather than reported as zero accuracy, since
// "no predictions landed here" is not evidence of anything.
func calibration(cases []scoredCase) []Bucket {
	const buckets = 10
	type acc struct {
		n       int
		sumP    float64
		correct int
	}
	bins := make([]acc, buckets)

	for _, c := range cases {
		p, ok := c.distribution[c.predicted]
		if !ok {
			continue
		}
		idx := int(p * buckets)
		if idx >= buckets {
			idx = buckets - 1
		}
		if idx < 0 {
			idx = 0
		}
		bins[idx].n++
		bins[idx].sumP += p
		if c.correct {
			bins[idx].correct++
		}
	}

	out := make([]Bucket, 0, buckets)
	for i, b := range bins {
		if b.n == 0 {
			continue
		}
		meanP := b.sumP / float64(b.n)
		observed := float64(b.correct) / float64(b.n)
		out = append(out, Bucket{
			LowerBound: float64(i) / buckets,
			UpperBound: float64(i+1) / buckets,
			Count:      b.n,
			MeanScore:  &meanP,
			Accuracy:   &observed,
		})
	}
	return out
}

// meanAbsoluteError measures how far a score landed from the expected level
// index, which is the error that matters on an ordinal scale: predicting
// "high" when the truth is "critical" is a smaller mistake than predicting
// "negligible", and plain accuracy cannot see the difference.
func meanAbsoluteError(cases []scoredCase) (float64, bool) {
	var sum float64
	var n int
	for _, c := range cases {
		if c.predictedScore == nil {
			continue
		}
		sum += math.Abs(*c.predictedScore - float64(c.expected.Index))
		n++
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

func latencyStats(values []int64) LatencyStats {
	if len(values) == 0 {
		return LatencyStats{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return LatencyStats{
		Count: len(sorted),
		P50MS: percentile(sorted, 0.50),
		P95MS: percentile(sorted, 0.95),
		MaxMS: sorted[len(sorted)-1],
	}
}

// percentile uses nearest-rank, which needs no interpolation and therefore
// always returns a latency that was actually observed.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
