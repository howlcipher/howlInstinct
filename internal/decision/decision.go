// Package decision orchestrates a single call: validate, lower, ask a
// provider, validate again, evaluate the caller's escalation rule, and build
// receipts.
//
// This package deliberately contains no policy. It evaluates the escalation
// rule the caller supplied and records the result; it never supplies a rule,
// never has a default threshold, and never decides what should happen next.
// Everything here is about producing an honest record of a judgment.
package decision

import (
	"context"
	"fmt"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

// Engine runs decisions against a provider.
type Engine struct {
	provider instinct.Provider
	limits   instinct.Limits
	timeout  time.Duration

	// now and newID are injectable so that tests can produce byte-stable
	// receipts. Production uses the real clock and random identifiers.
	now   func() time.Time
	newID receipt.IDFunc
}

// Option configures an Engine.
type Option func(*Engine)

// WithLimits overrides the request limits.
func WithLimits(l instinct.Limits) Option { return func(e *Engine) { e.limits = l } }

// WithTimeout overrides the per-call deadline.
func WithTimeout(d time.Duration) Option { return func(e *Engine) { e.timeout = d } }

// WithClock overrides the clock, for deterministic tests.
func WithClock(f func() time.Time) Option { return func(e *Engine) { e.now = f } }

// WithIDFunc overrides identifier generation, for deterministic tests.
func WithIDFunc(f receipt.IDFunc) Option { return func(e *Engine) { e.newID = f } }

// DefaultTimeout bounds a single decision call.
//
// A bounded default matters more than its exact value: without one, a
// provider that accepts a connection and then stalls would hold a caller
// open indefinitely, which is the cheapest denial of service there is.
const DefaultTimeout = 30 * time.Second

// New returns an Engine over the given provider.
func New(p instinct.Provider, opts ...Option) *Engine {
	e := &Engine{
		provider: p,
		limits:   instinct.DefaultLimits(),
		timeout:  DefaultTimeout,
		now:      time.Now,
		newID:    receipt.NewID,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Result is everything one decision call produced.
type Result struct {
	Response instinct.Response
	Receipts []receipt.DecisionReceipt
	BatchID  string
}

// Decide runs one request end to end.
//
// On provider failure it returns both an error and a Result whose receipts
// record UNAVAILABLE or ERROR for every question asked. A failed decision is
// still a decision that was attempted, and an audit trail that simply omits
// the attempt is misleading.
func (e *Engine) Decide(ctx context.Context, req instinct.Request) (Result, error) {
	if err := req.Validate(e.limits); err != nil {
		return Result{}, err
	}

	batchID := e.newID()
	started := e.now()

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// Lower before the provider sees the request. Classify is HowlInstinct
	// vocabulary; no adapter has a case for it, and this is the single point
	// that guarantees none ever needs one.
	lowered, originalTypes := lower(req)

	resp, err := e.provider.Decide(ctx, lowered)
	latency := e.now().Sub(started).Milliseconds()

	meta := receipt.Meta{
		BatchID:   batchID,
		Timestamp: started,
		Provider:  e.provider.Name(),
		Model:     resp.Model,
		Endpoint:  resp.Endpoint,
		LatencyMS: latency,
		Usage:     resp.Usage,
	}
	if meta.Provider == "" {
		meta.Provider = "unknown"
	}

	if err != nil {
		failed := failureResponse(req, err)
		failed.Provider = meta.Provider
		failed.LatencyMS = latency
		receipts, buildErr := receipt.Build(req, failed, meta, e.newID)
		if buildErr != nil {
			return Result{}, fmt.Errorf("building receipts for a failed decision: %w", buildErr)
		}
		return Result{Response: failed, Receipts: receipts, BatchID: batchID}, err
	}

	// Restore the caller's vocabulary and record the lowering.
	restore(&resp, originalTypes)

	// Evaluate the caller's own rule. With no rule, nothing is escalated.
	for id, j := range resp.Judgments {
		j.ApplyEscalation(req.Escalation[id])
		resp.Judgments[id] = j
	}

	resp.Provider = meta.Provider
	resp.LatencyMS = latency

	receipts, err := receipt.Build(req, resp, meta, e.newID)
	if err != nil {
		return Result{}, fmt.Errorf("building receipts: %w", err)
	}
	return Result{Response: resp, Receipts: receipts, BatchID: batchID}, nil
}

// lower rewrites classify questions as choice questions and remembers what
// the caller originally asked for.
func lower(req instinct.Request) (instinct.Request, map[string]instinct.DecisionType) {
	originals := make(map[string]instinct.DecisionType, len(req.Questions))
	questions := make(map[string]instinct.Question, len(req.Questions))
	for id, q := range req.Questions {
		originals[id] = q.Type
		q.Type = q.Type.Lower()
		questions[id] = q
	}
	out := req
	out.Questions = questions
	return out, originals
}

// restore puts the caller's declared type back onto each judgment and records
// the primitive that was actually asked for.
func restore(resp *instinct.Response, originals map[string]instinct.DecisionType) {
	for id, j := range resp.Judgments {
		original, ok := originals[id]
		if !ok {
			continue
		}
		j.Type = original
		if lowered := original.Lower(); lowered != original {
			j.CompiledTo = lowered
		} else {
			j.CompiledTo = ""
		}
		resp.Judgments[id] = j
	}
}

// failureResponse synthesises judgments describing why nothing was decided.
//
// The outcome distinguishes an unreachable provider from one that answered
// badly, because those call for different responses: the first is an
// operational problem, the second is an integrity problem.
func failureResponse(req instinct.Request, err error) instinct.Response {
	outcome := instinct.OutcomeUnavailable
	if kind, ok := instinct.KindOf(err); ok && kind == instinct.KindProviderResponseInvalid {
		outcome = instinct.OutcomeError
	}
	judgments := make(map[string]instinct.Judgment, len(req.Questions))
	for id, q := range req.Questions {
		j := instinct.Judgment{
			QuestionID: id,
			Type:       q.Type,
			Outcome:    outcome,
			Error:      err.Error(),
		}
		if lowered := q.Type.Lower(); lowered != q.Type {
			j.CompiledTo = lowered
		}
		judgments[id] = j
	}
	return instinct.Response{Judgments: judgments}
}

// Judgments returns the response's judgments, for brevity at call sites.
func (r Result) Judgments() map[string]instinct.Judgment { return r.Response.Judgments }
