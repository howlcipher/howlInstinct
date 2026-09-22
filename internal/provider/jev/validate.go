package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// distributionTolerance is how far a probability distribution may sum from 1
// before it is rejected. It accommodates ordinary floating point and
// rounding in a provider's serialization, and nothing more.
const distributionTolerance = 1e-6

// invalid builds a response-validation error. Every check in this file
// produces one, so that a caller can distinguish "the provider could not be
// reached", which is an operational problem, from "the provider said
// something I cannot trust", which is an integrity problem.
func invalid(format string, args ...any) *instinct.Error {
	return instinct.Errorf(instinct.KindProviderResponseInvalid, "jev", format, args...)
}

// parseResponse turns raw bytes from the endpoint into judgments, refusing
// anything that fails any check.
//
// The ordering matters. Cheap structural checks come before expensive
// semantic ones, and identity is established before any value is read: there
// is no point range-checking a probability that belongs to a question nobody
// asked.
func parseResponse(body []byte, req instinct.Request, strict bool) (instinct.Response, error) {
	var out instinct.Response

	wire, err := decodeStrict[wireResponse](body, strict)
	if err != nil {
		return out, invalid("decoding response envelope: %v", err)
	}

	// The answer set must match the question set exactly. Without this, a
	// buggy or hostile endpoint could answer a question that was never asked
	// and the receipt would still claim the original question was the one
	// judged. It also catches a provider silently dropping part of a batch.
	if err := checkAnswerIdentity(req, wire.Answers); err != nil {
		return out, err
	}

	judgments := make(map[string]instinct.Judgment, len(wire.Answers))
	for _, id := range req.QuestionIDs() {
		q := req.Questions[id]
		answer, err := decodeStrict[wireAnswer](wire.Answers[id], strict)
		if err != nil {
			return out, invalid("decoding answer for question %q: %v", id, err)
		}
		j, err := judgmentFor(id, q, answer)
		if err != nil {
			return out, err
		}
		judgments[id] = j
	}

	out.Judgments = judgments
	out.Model = wire.Model
	if wire.Usage != nil {
		out.Usage = &instinct.Usage{
			InputTokens:  wire.Usage.InputTokens,
			OutputTokens: wire.Usage.OutputTokens,
		}
	}
	return out, nil
}

// decodeStrict decodes JSON into T, optionally rejecting unknown fields.
func decodeStrict[T any](raw []byte, strict bool) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, fmt.Errorf("value is absent")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// checkAnswerIdentity requires the answered ids to equal the asked ids.
func checkAnswerIdentity(req instinct.Request, answers map[string]json.RawMessage) error {
	var missing, unexpected []string
	for _, id := range req.QuestionIDs() {
		if _, ok := answers[id]; !ok {
			missing = append(missing, id)
		}
	}
	for id := range answers {
		if _, ok := req.Questions[id]; !ok {
			unexpected = append(unexpected, id)
		}
	}
	sort.Strings(unexpected)

	switch {
	case len(missing) > 0 && len(unexpected) > 0:
		return invalid("provider answered a different set of questions: missing %v, unexpected %v",
			missing, unexpected)
	case len(missing) > 0:
		return invalid("provider did not answer %v", missing)
	case len(unexpected) > 0:
		return invalid("provider answered questions that were never asked: %v", unexpected)
	}
	return nil
}

// judgmentFor validates one answer against the question it claims to answer.
func judgmentFor(id string, q instinct.Question, a wireAnswer) (instinct.Judgment, error) {
	j := instinct.Judgment{
		QuestionID: id,
		Type:       q.Type,
		Outcome:    instinct.OutcomeAcceptableConfidence,
	}
	if lowered := q.Type.Lower(); lowered != q.Type {
		j.CompiledTo = lowered
	}

	// Confidence is preserved exactly as reported, and only when reported.
	// It is never derived from a probability: they are different quantities,
	// and silently substituting one for the other is precisely the failure
	// this project exists to prevent.
	if a.Confidence != nil {
		if err := checkUnit(id, "confidence", *a.Confidence); err != nil {
			return j, err
		}
		j.ProviderConfidence = a.Confidence
	}

	switch q.Type.Lower() {
	case instinct.TypeNoul:
		return noulJudgment(id, j, a)
	case instinct.TypeChoice:
		return choiceJudgment(id, q, j, a)
	case instinct.TypeScore:
		return scoreJudgment(id, q, j, a)
	default:
		return j, invalid("question %q has type %q, which this adapter cannot ask", id, q.Type)
	}
}

func noulJudgment(id string, j instinct.Judgment, a wireAnswer) (instinct.Judgment, error) {
	if a.Noul == nil {
		return j, invalid("answer for noul question %q carries no noul probability", id)
	}
	if err := rejectForeignFields(id, "noul", a.Choice != nil, a.Score != nil, len(a.Legend) > 0); err != nil {
		return j, err
	}
	if err := checkUnit(id, "noul", *a.Noul); err != nil {
		return j, err
	}
	j.ProbabilityYes = a.Noul
	j.Bool = instinct.Bool(instinct.BoolFromProbabilityYes(*a.Noul))
	if m, ok := instinct.MarginFromProbabilityYes(*a.Noul); ok {
		j.InstinctMargin = instinct.Float(m)
	}
	return j, nil
}

func choiceJudgment(id string, q instinct.Question, j instinct.Judgment, a wireAnswer) (instinct.Judgment, error) {
	if a.Choice == nil {
		return j, invalid("answer for choice question %q carries no choice", id)
	}
	if err := rejectForeignFields(id, "choice", a.Noul != nil, a.Score != nil, len(a.Legend) > 0); err != nil {
		return j, err
	}

	allowed := make(map[string]bool, len(q.Options))
	for _, name := range q.OptionNames() {
		allowed[name] = true
	}
	if !allowed[*a.Choice] {
		return j, invalid("question %q was answered %q, which is not one of its options %v",
			id, *a.Choice, q.OptionNames())
	}
	j.Choice = a.Choice

	if len(a.Probabilities) == 0 {
		// Absent rather than wrong. Downstream metrics that need a
		// distribution will report themselves unavailable rather than
		// inventing one.
		return j, nil
	}

	dist, err := decodeStrict[map[string]float64](a.Probabilities, false)
	if err != nil {
		return j, invalid("question %q returned probabilities that are not an object of numbers: %v", id, err)
	}
	if err := checkDistributionKeys(id, dist, allowed, q.OptionNames()); err != nil {
		return j, err
	}
	if err := checkDistributionValues(id, mapValues(dist)); err != nil {
		return j, err
	}
	j.Probabilities = dist
	if m, ok := instinct.MarginFromDistribution(dist); ok {
		j.InstinctMargin = instinct.Float(m)
	}
	return j, nil
}

func scoreJudgment(id string, q instinct.Question, j instinct.Judgment, a wireAnswer) (instinct.Judgment, error) {
	if a.Score == nil {
		return j, invalid("answer for score question %q carries no score", id)
	}
	if err := rejectForeignFields(id, "score", a.Noul != nil, a.Choice != nil, false); err != nil {
		return j, err
	}

	levels := q.LevelNames()
	if !instinct.IsFinite(*a.Score) {
		return j, invalid("question %q returned a non-finite score", id)
	}
	// The scale is defined by the levels the caller supplied, so a score
	// outside it is meaningless regardless of what the provider intended.
	if max := float64(len(levels) - 1); *a.Score < 0 || *a.Score > max {
		return j, invalid("question %q returned score %v, outside the scale [0,%v] its %d levels define",
			id, *a.Score, max, len(levels))
	}
	j.Score = a.Score

	if len(a.Legend) > 0 {
		if len(a.Legend) != len(levels) {
			return j, invalid("question %q returned a legend of %d entries for %d levels",
				id, len(a.Legend), len(levels))
		}
		for i := range levels {
			if a.Legend[i] != levels[i] {
				return j, invalid("question %q returned legend entry %d as %q, but level %d is %q",
					id, i, a.Legend[i], i, levels[i])
			}
		}
	}
	j.Legend = levels

	if len(a.Probabilities) == 0 {
		return j, nil
	}
	// For score the distribution is an ordered array aligned with the levels,
	// not an object: the contract is asymmetric here and the adapter matches it.
	ordered, err := decodeStrict[[]float64](a.Probabilities, false)
	if err != nil {
		return j, invalid("question %q returned probabilities that are not an array of numbers: %v", id, err)
	}
	if len(ordered) != len(levels) {
		return j, invalid("question %q returned %d probabilities for %d levels",
			id, len(ordered), len(levels))
	}
	if err := checkDistributionValues(id, ordered); err != nil {
		return j, err
	}
	dist := make(map[string]float64, len(levels))
	for i, name := range levels {
		dist[name] = ordered[i]
	}
	j.Probabilities = dist
	if m, ok := instinct.MarginFromDistribution(dist); ok {
		j.InstinctMargin = instinct.Float(m)
	}
	return j, nil
}

// rejectForeignFields refuses an answer carrying values belonging to a
// different question type. A choice answer that also contains a noul
// probability is evidence the provider answered something other than what was
// asked, and guessing which half to believe is not an option.
func rejectForeignFields(id, want string, foreign ...bool) error {
	for _, present := range foreign {
		if present {
			return invalid("answer for %s question %q carries fields belonging to another question type", want, id)
		}
	}
	return nil
}

func checkUnit(id, field string, v float64) error {
	if !instinct.IsFinite(v) {
		return invalid("question %q returned a non-finite %s", id, field)
	}
	if v < 0 || v > 1 {
		return invalid("question %q returned %s %v, outside [0,1]", id, field, v)
	}
	return nil
}

func checkDistributionKeys(id string, dist map[string]float64, allowed map[string]bool, names []string) error {
	var unknown, missing []string
	for k := range dist {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	for _, name := range names {
		if _, ok := dist[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return invalid("question %q returned probabilities for options that do not exist: %v", id, unknown)
	}
	if len(missing) > 0 {
		return invalid("question %q returned no probability for options %v", id, missing)
	}
	return nil
}

func checkDistributionValues(id string, values []float64) error {
	var sum float64
	for _, v := range values {
		if !instinct.IsFinite(v) {
			return invalid("question %q returned a non-finite probability", id)
		}
		if v < 0 || v > 1 {
			return invalid("question %q returned probability %v, outside [0,1]", id, v)
		}
		sum += v
	}
	if diff := sum - 1; diff > distributionTolerance || diff < -distributionTolerance {
		return invalid("question %q returned probabilities summing to %v, not 1", id, sum)
	}
	return nil
}

func mapValues(m map[string]float64) []float64 {
	out := make([]float64, 0, len(m))
	// Sorted so that an error message about the distribution is reproducible.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}
