package eval

import (
	"context"
	"math"
	"time"

	"github.com/howlcipher/howlinstinct/internal/decision"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// ReportSchema identifies the machine-readable evaluation report.
const ReportSchema = "howlinstinct.eval_run/v1"

// Report is one evaluation run, in full.
//
// It records what was measured and what it was measured against, because a
// metric without its provenance is not evidence of anything: the same suite
// against a different model on a different day is a different result.
type Report struct {
	Schema  string      `json:"schema"`
	Dataset DatasetInfo `json:"dataset"`

	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`

	StartedAt  string `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`

	// EscalationRuleSupplied records whether the caller supplied a threshold.
	// Without it, an escalation rate of zero means "nothing was asked for",
	// not "nothing was uncertain", and a reader must be able to tell.
	EscalationRuleSupplied bool `json:"escalation_rule_supplied"`

	Metrics  Metrics   `json:"metrics"`
	Failures []Failure `json:"failures,omitempty"`

	// Caveat is emitted with every report. Evaluation numbers are routinely
	// quoted out of context, so the caveat travels with the data.
	Caveat string `json:"caveat"`
}

// DatasetInfo identifies exactly which bytes were measured.
type DatasetInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Cases   int    `json:"cases"`
}

// Failure records a case that produced no usable judgment.
type Failure struct {
	CaseID string `json:"case_id"`
	Reason string `json:"reason"`
}

// Options configures a run.
type Options struct {
	// Escalation is a caller-supplied rule applied to every case. The zero
	// value means no rule, and therefore no escalation, which is the only
	// honest default: the harness has no view on what counts as confident.
	Escalation instinct.EscalationRule

	ProviderName string
	Model        string
	Endpoint     string

	Now func() time.Time
}

// Run measures a provider against a dataset, one case at a time.
//
// Cases run sequentially rather than concurrently. The harness is bounded
// work against someone else's rate-limited service, and a parallel harness
// that trips rate limiting measures the rate limiter rather than the model.
func Run(ctx context.Context, ds Dataset, engine *decision.Engine, opts Options) (Report, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	started := now()

	report := Report{
		Schema: ReportSchema,
		Dataset: DatasetInfo{
			Name: ds.Name, Path: ds.Path, Version: ds.Version,
			SHA256: ds.SHA256, Cases: len(ds.Cases),
		},
		Provider:               opts.ProviderName,
		Model:                  opts.Model,
		Endpoint:               opts.Endpoint,
		StartedAt:              started.UTC().Format(time.RFC3339),
		EscalationRuleSupplied: !opts.Escalation.IsZero(),
		Caveat: "These numbers describe one provider against one dataset at one moment. " +
			"They are not a claim that this decision class is production ready, and they " +
			"do not transfer to a different model, dataset, or population.",
	}

	var scored []scoredCase
	var errored int
	scoreType := false

	for _, c := range ds.Cases {
		if err := ctx.Err(); err != nil {
			return report, instinct.Wrap(instinct.KindTimeout, "eval", err,
				"evaluation cancelled after %d cases", len(scored)+errored)
		}

		question, expected, err := c.prepare(ds.Question)
		if err != nil {
			errored++
			report.Failures = append(report.Failures, Failure{CaseID: c.ID, Reason: err.Error()})
			continue
		}
		if question.Type.Lower() == instinct.TypeScore {
			scoreType = true
		}

		req := instinct.Request{
			State:     c.State,
			Questions: map[string]instinct.Question{c.ID: question},
		}
		if !opts.Escalation.IsZero() {
			req.Escalation = map[string]instinct.EscalationRule{c.ID: opts.Escalation}
		}

		res, err := engine.Decide(ctx, req)
		if err != nil {
			errored++
			report.Failures = append(report.Failures, Failure{CaseID: c.ID, Reason: err.Error()})
			continue
		}
		j, ok := res.Response.Judgments[c.ID]
		if !ok || j.Outcome == instinct.OutcomeUnavailable || j.Outcome == instinct.OutcomeError {
			errored++
			report.Failures = append(report.Failures, Failure{
				CaseID: c.ID, Reason: failureReason(j, ok),
			})
			continue
		}

		sc, err := score(question, expected, j, res.Response.LatencyMS)
		if err != nil {
			errored++
			report.Failures = append(report.Failures, Failure{CaseID: c.ID, Reason: err.Error()})
			continue
		}
		scored = append(scored, sc)
	}

	report.Metrics = computeMetrics(scored, len(ds.Cases), errored, scoreType)
	report.DurationMS = now().Sub(started).Milliseconds()
	return report, nil
}

func failureReason(j instinct.Judgment, present bool) string {
	if !present {
		return "provider returned no judgment for this case"
	}
	if j.Error != "" {
		return j.Error
	}
	return string(j.Outcome)
}

// prepare resolves a case's question and ground-truth label.
func (c Case) prepare(fallback *Question) (instinct.Question, expectation, error) {
	q := c.resolveQuestion(fallback)
	if q == nil {
		return instinct.Question{}, expectation{},
			instinct.Errorf(instinct.KindInvalidInput, "eval", "case %q has no question", c.ID)
	}
	question, err := q.toInstinct()
	if err != nil {
		return question, expectation{}, err
	}
	exp, err := normalizeExpected(question, c.Expected)
	return question, exp, err
}

// score reduces a judgment to the comparable form the metrics consume.
func score(q instinct.Question, exp expectation, j instinct.Judgment, latencyMS int64) (scoredCase, error) {
	sc := scoredCase{expected: exp, escalated: j.NeedsEscalation, latencyMS: latencyMS}

	switch q.Type.Lower() {
	case instinct.TypeNoul:
		if j.Bool == nil {
			return sc, instinct.Errorf(instinct.KindProviderResponseInvalid, "eval",
				"noul judgment carried no answer")
		}
		sc.predicted = boolLabel(*j.Bool)
		if j.ProbabilityYes != nil {
			// A noul's single probability is a full two-class distribution,
			// so it can be scored with the same machinery as a choice.
			p := *j.ProbabilityYes
			sc.distribution = map[string]float64{"true": p, "false": 1 - p}
		}

	case instinct.TypeChoice:
		if j.Choice == nil {
			return sc, instinct.Errorf(instinct.KindProviderResponseInvalid, "eval",
				"choice judgment carried no answer")
		}
		sc.predicted = *j.Choice
		sc.distribution = j.Probabilities

	case instinct.TypeScore:
		if j.Score == nil {
			return sc, instinct.Errorf(instinct.KindProviderResponseInvalid, "eval",
				"score judgment carried no answer")
		}
		levels := q.LevelNames()
		idx := int(math.Round(*j.Score))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(levels) {
			idx = len(levels) - 1
		}
		sc.predicted = levels[idx]
		sc.predictedScore = j.Score
		sc.distribution = j.Probabilities
	}

	sc.correct = sc.predicted == exp.Label
	return sc, nil
}
