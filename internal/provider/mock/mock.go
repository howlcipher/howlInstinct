// Package mock provides a deterministic, offline decision provider.
//
// Its purpose is to make the entire normal test suite, the CLI, the eval
// harness, and the dogfood example runnable with no network access, no
// credentials, and no inference hardware. It is wired in as the default
// provider so that a fresh checkout does something useful immediately.
//
// What it is NOT is a semantic model. The judgments it returns come from a
// trivial lexical overlap heuristic: it counts shared word stems between the
// state and each candidate label. That is a legitimate baseline to measure a
// real provider against, and it is an honest one, but it understands nothing.
// Any evaluation number produced against this provider measures the harness,
// not a model.
//
// The mock deliberately reproduces one quirk of the real Jev-compatible
// contract: a noul answer carries no confidence value, because the
// probability is itself the uncertainty signal. Reproducing the absence here
// is what keeps the rest of the system honest about handling it, rather than
// only discovering the gap against a live endpoint.
package mock

import (
	"context"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// Name is the adapter's stable identifier, recorded in receipts.
const Name = "mock"

// Model is the pseudo-model identifier this provider reports. It is named to
// be self-describing in any receipt that carries it.
const Model = "mock-lexical-baseline-v1"

// Behavior injects deliberate faults so that error handling can be tested
// without a network. The zero value behaves normally.
type Behavior struct {
	// FailWith, if non-nil, is returned instead of a response.
	FailWith error

	// Delay is slept before answering, honouring the context deadline. It
	// exists so timeout handling can be exercised deterministically.
	Delay time.Duration

	// OmitJudgments names question ids the provider will silently fail to
	// answer, modelling a provider that drops part of a batch.
	OmitJudgments []string

	// Scripted overrides the heuristic with fixed answers, keyed by question
	// id. Used by the dogfood example so its narrative is stable.
	Scripted map[string]instinct.Judgment
}

// Provider is the deterministic offline provider.
type Provider struct {
	behavior Behavior
}

// New returns a mock provider with default behavior.
func New() *Provider { return &Provider{} }

// NewWithBehavior returns a mock provider with fault injection configured.
func NewWithBehavior(b Behavior) *Provider { return &Provider{behavior: b} }

// Name identifies the adapter.
func (p *Provider) Name() string { return Name }

// Decide answers every question in req.
func (p *Provider) Decide(ctx context.Context, req instinct.Request) (instinct.Response, error) {
	start := time.Now()

	if p.behavior.Delay > 0 {
		select {
		case <-time.After(p.behavior.Delay):
		case <-ctx.Done():
			return instinct.Response{}, instinct.Wrap(
				instinct.KindTimeout, "mock", ctx.Err(), "deadline elapsed while deciding")
		}
	}
	if err := ctx.Err(); err != nil {
		return instinct.Response{}, instinct.Wrap(
			instinct.KindTimeout, "mock", err, "deadline elapsed before deciding")
	}
	if p.behavior.FailWith != nil {
		return instinct.Response{}, p.behavior.FailWith
	}

	omitted := make(map[string]bool, len(p.behavior.OmitJudgments))
	for _, id := range p.behavior.OmitJudgments {
		omitted[id] = true
	}

	judgments := make(map[string]instinct.Judgment, len(req.Questions))
	for _, id := range req.QuestionIDs() {
		if omitted[id] {
			continue
		}
		if scripted, ok := p.behavior.Scripted[id]; ok {
			scripted.QuestionID = id
			judgments[id] = scripted
			continue
		}
		judgments[id] = p.answer(id, req.State, req.Questions[id])
	}

	return instinct.Response{
		Judgments: judgments,
		Provider:  Name,
		Model:     Model,
		LatencyMS: time.Since(start).Milliseconds(),
		Usage: &instinct.Usage{
			// Reported so that receipt and eval plumbing for usage is
			// exercised offline. These are counts of tokens this provider
			// notionally read, not a billing figure.
			InputTokens:  instinct.Int64(int64(len(strings.Fields(req.State)))),
			OutputTokens: instinct.Int64(0),
		},
	}, nil
}

func (p *Provider) answer(id, state string, q instinct.Question) instinct.Judgment {
	j := instinct.Judgment{QuestionID: id, Type: q.Type, Outcome: instinct.OutcomeAcceptableConfidence}
	if lowered := q.Type.Lower(); lowered != q.Type {
		j.CompiledTo = lowered
	}
	stateTokens := tokenize(state)

	switch q.Type.Lower() {
	case instinct.TypeNoul:
		// Weigh the question's own wording, plus whatever the caller said
		// "true" means, against the state.
		yes := affinity(stateTokens, q.Instructions+" "+q.TrueMeaning)
		no := affinity(stateTokens, q.FalseMeaning)
		p := squash(yes-no, seedOf(id, state, q.Instructions))
		j.ProbabilityYes = instinct.Float(p)
		j.Bool = instinct.Bool(p >= 0.5)
		if m, ok := instinct.MarginFromProbabilityYes(p); ok {
			j.InstinctMargin = instinct.Float(m)
		}
		// Deliberately no ProviderConfidence: a noul has none.

	case instinct.TypeChoice:
		names := q.OptionNames()
		weights := make([]float64, len(q.Options))
		for i, o := range q.Options {
			weights[i] = affinity(stateTokens, o.Name+" "+o.Description)
		}
		dist := normalize(weights, seedOf(id, state, q.Instructions))
		j.Probabilities = zip(names, dist)
		best := argmax(dist)
		j.Choice = instinct.String(names[best])
		j.ProviderConfidence = instinct.Float(dist[best])
		if m, ok := instinct.MarginFromDistribution(j.Probabilities); ok {
			j.InstinctMargin = instinct.Float(m)
		}

	case instinct.TypeScore:
		names := q.LevelNames()
		weights := make([]float64, len(q.Levels))
		for i, l := range q.Levels {
			weights[i] = affinity(stateTokens, l.Name+" "+l.Description)
		}
		dist := normalize(weights, seedOf(id, state, q.Instructions))
		j.Probabilities = zip(names, dist)
		j.Legend = names
		// The score is the probability-weighted level index, matching the
		// upstream definition: score = sum of i * p(i).
		var score float64
		for i, pr := range dist {
			score += float64(i) * pr
		}
		j.Score = instinct.Float(score)
		j.ProviderConfidence = instinct.Float(dist[argmax(dist)])
		if m, ok := instinct.MarginFromDistribution(j.Probabilities); ok {
			j.InstinctMargin = instinct.Float(m)
		}
	}
	return j
}

// tokenize reduces text to a set of lowercase alphanumeric word stems.
func tokenize(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(f) < 3 {
			continue // drop articles and noise
		}
		out[stem(f)] = true
	}
	return out
}

// stem is a crude suffix trim. It is not linguistics; it just makes "outages"
// and "outage" collide.
func stem(w string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(suffix)+2 && strings.HasSuffix(w, suffix) {
			return strings.TrimSuffix(w, suffix)
		}
	}
	return w
}

// affinity counts how much of label appears in the state.
func affinity(stateTokens map[string]bool, label string) float64 {
	labelTokens := tokenize(label)
	if len(labelTokens) == 0 {
		return 0
	}
	var hits float64
	for tok := range labelTokens {
		if stateTokens[tok] {
			hits++
		}
	}
	return hits
}

// normalize turns raw weights into a proper probability distribution.
//
// Smoothing keeps every option strictly positive, which matters because log
// loss is undefined at zero and a provider that reports an impossible outcome
// with certainty is rarely telling the truth. The seed breaks ties
// deterministically so that identical inputs always produce identical output.
func normalize(weights []float64, seed uint64) []float64 {
	const smoothing = 0.35
	out := make([]float64, len(weights))
	var total float64
	for i, w := range weights {
		jitter := float64((seed>>(uint(i)%32))&0xff) / 255.0 * 0.01
		out[i] = w + smoothing + jitter
		total += out[i]
	}
	if total == 0 {
		for i := range out {
			out[i] = 1 / float64(len(out))
		}
		return out
	}
	for i := range out {
		out[i] /= total
	}
	return out
}

// squash maps a signed lexical margin into (0,1), never reaching either end:
// this provider is a word counter and should never claim certainty.
func squash(x float64, seed uint64) float64 {
	jitter := (float64(seed&0xff)/255.0 - 0.5) * 0.02
	p := 1/(1+math.Exp(-1.6*x)) + jitter
	return math.Min(0.99, math.Max(0.01, p))
}

func seedOf(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func zip(names []string, dist []float64) map[string]float64 {
	out := make(map[string]float64, len(names))
	for i, n := range names {
		out[n] = dist[i]
	}
	return out
}

// argmax returns the index of the largest value, breaking ties toward the
// lowest index so the result never depends on iteration order.
func argmax(dist []float64) int {
	best := 0
	for i, v := range dist {
		if v > dist[best] {
			best = i
		}
	}
	return best
}

// SortedNames is a small helper for callers that need deterministic ordering
// of a distribution's keys.
func SortedNames(dist map[string]float64) []string {
	out := make([]string, 0, len(dist))
	for k := range dist {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
