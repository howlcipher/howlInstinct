package receipt

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// Schema identifies this receipt contract and its version. It follows the
// ecosystem convention of a namespaced identifier carrying the version, and
// is echoed inside every instance so that a receipt found on disk, with no
// surrounding context, still says what it is.
const Schema = "howlinstinct.decision_receipt/v1"

// Version is the human-facing receipt version, tracked alongside Schema so
// that additive revisions are visible without parsing the schema string.
const Version = 1

// DecisionReceipt is the durable record of one judgment.
//
// One receipt describes one question's judgment. A batch produces several
// receipts sharing a BatchID, rather than one receipt describing many
// judgments, because receipts are meant to be filed, referenced, and reasoned
// about individually.
type DecisionReceipt struct {
	Schema         string `json:"schema"`
	ReceiptVersion int    `json:"receipt_version"`

	// DecisionID is unique per judgment. BatchID is shared by every judgment
	// produced by the same provider call.
	DecisionID string `json:"decision_id"`
	BatchID    string `json:"batch_id"`

	Timestamp string `json:"timestamp"`

	QuestionID string                `json:"question_id"`
	Type       instinct.DecisionType `json:"decision_type"`
	// CompiledTo records the primitive the provider was actually asked for,
	// set only when it differs from Type (classify lowers to choice).
	CompiledTo instinct.DecisionType `json:"compiled_to,omitempty"`

	// StateHash and QuestionHash identify the inputs without reproducing
	// them. They are what makes two decisions comparable, and what lets a
	// caller prove later which input a judgment was about.
	StateHash    string `json:"state_hash"`
	QuestionHash string `json:"question_hash"`

	// State is present only when the caller explicitly opted in. State
	// routinely contains logs, customer records, or credentials, and a
	// receipt is durable, so retention is never the default.
	State string `json:"state,omitempty"`

	Outcome instinct.Outcome `json:"outcome"`

	// The judgment itself. Every provider-supplied number is a pointer so
	// that an absent value stays absent rather than becoming a zero that
	// nobody reported. See instinct.Judgment.
	Bool               *bool              `json:"bool,omitempty"`
	Choice             *string            `json:"choice,omitempty"`
	Score              *float64           `json:"score,omitempty"`
	Legend             []string           `json:"legend,omitempty"`
	ProbabilityYes     *float64           `json:"probability_yes,omitempty"`
	Probabilities      map[string]float64 `json:"probabilities,omitempty"`
	ProviderConfidence *float64           `json:"provider_confidence,omitempty"`
	InstinctMargin     *float64           `json:"instinct_margin,omitempty"`

	NeedsEscalation  bool   `json:"needs_escalation"`
	EscalationReason string `json:"escalation_reason,omitempty"`

	Provider      string `json:"provider"`
	ProviderModel string `json:"provider_model,omitempty"`
	// ProviderEndpoint is always credential-free. See SafeEndpoint.
	ProviderEndpoint string `json:"provider_endpoint,omitempty"`

	LatencyMS int64           `json:"latency_ms"`
	Usage     *instinct.Usage `json:"usage,omitempty"`

	CorrelationID string `json:"correlation_id,omitempty"`
	WorkflowID    string `json:"workflow_id,omitempty"`

	Error string `json:"error,omitempty"`
}

// Meta carries the per-call facts a receipt needs that a judgment does not
// itself know.
type Meta struct {
	BatchID   string
	Timestamp time.Time
	Provider  string
	Model     string
	Endpoint  string
	LatencyMS int64
	Usage     *instinct.Usage
}

// IDFunc produces decision identifiers. It is injectable so that tests can
// pin identifiers and assert on complete receipt bytes; production uses
// NewID.
type IDFunc func() string

// Build assembles the receipts for one provider call.
//
// Receipts are returned in sorted question-id order so that writing a batch
// to a file, or hashing the batch as a whole, is reproducible.
func Build(req instinct.Request, resp instinct.Response, meta Meta, newID IDFunc) ([]DecisionReceipt, error) {
	if newID == nil {
		newID = NewID
	}
	stateHash := HashString(req.State)
	ts := meta.Timestamp.UTC().Format(time.RFC3339Nano)

	ids := req.QuestionIDs()
	out := make([]DecisionReceipt, 0, len(ids))
	for _, id := range ids {
		question := req.Questions[id]
		qHash, err := HashCanonical(question)
		if err != nil {
			return nil, fmt.Errorf("hashing question %q: %w", id, err)
		}

		j, ok := resp.Judgments[id]
		if !ok {
			// A provider that answered a different set of questions than it
			// was asked is a validation failure upstream of here, but a
			// receipt still has to exist for the question that was asked.
			j = instinct.Judgment{
				QuestionID: id,
				Type:       question.Type,
				Outcome:    instinct.OutcomeUnavailable,
				Error:      "provider returned no judgment for this question",
			}
		}

		r := DecisionReceipt{
			Schema:             Schema,
			ReceiptVersion:     Version,
			DecisionID:         newID(),
			BatchID:            meta.BatchID,
			Timestamp:          ts,
			QuestionID:         id,
			Type:               question.Type,
			StateHash:          stateHash,
			QuestionHash:       qHash,
			Outcome:            j.Outcome,
			Bool:               j.Bool,
			Choice:             j.Choice,
			Score:              j.Score,
			Legend:             j.Legend,
			ProbabilityYes:     j.ProbabilityYes,
			Probabilities:      j.Probabilities,
			ProviderConfidence: j.ProviderConfidence,
			InstinctMargin:     j.InstinctMargin,
			NeedsEscalation:    j.NeedsEscalation,
			EscalationReason:   j.EscalationReason,
			Provider:           meta.Provider,
			ProviderModel:      meta.Model,
			ProviderEndpoint:   SafeEndpoint(meta.Endpoint),
			LatencyMS:          meta.LatencyMS,
			Usage:              meta.Usage,
			CorrelationID:      req.CorrelationID,
			WorkflowID:         req.WorkflowID,
			Error:              j.Error,
		}
		if lowered := question.Type.Lower(); lowered != question.Type {
			r.CompiledTo = lowered
		}
		if req.RetainState {
			r.State = req.State
		}
		out = append(out, r)
	}
	return out, nil
}

// Validate checks a receipt's self-consistency. It checks the schema
// identifier first, on the principle that a document claiming to be something
// else should be rejected before its fields are interpreted at all.
func (r DecisionReceipt) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("receipt declares schema %q, want %q", r.Schema, Schema)
	}
	if r.ReceiptVersion != Version {
		return fmt.Errorf("receipt declares version %d, want %d", r.ReceiptVersion, Version)
	}
	for _, f := range []struct{ name, val string }{
		{"decision_id", r.DecisionID},
		{"batch_id", r.BatchID},
		{"timestamp", r.Timestamp},
		{"question_id", r.QuestionID},
		{"state_hash", r.StateHash},
		{"question_hash", r.QuestionHash},
		{"provider", r.Provider},
	} {
		if f.val == "" {
			return fmt.Errorf("receipt is missing required field %q", f.name)
		}
	}
	if !r.Type.IsValid() {
		return fmt.Errorf("receipt has unknown decision_type %q", r.Type)
	}
	if !r.Outcome.IsValid() {
		return fmt.Errorf("receipt has unknown outcome %q", r.Outcome)
	}
	if r.CompiledTo != "" && !r.CompiledTo.IsValid() {
		return fmt.Errorf("receipt has unknown compiled_to %q", r.CompiledTo)
	}
	if r.EscalationReason != "" && !r.NeedsEscalation {
		return fmt.Errorf("receipt carries an escalation reason but needs_escalation is false")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.Timestamp); err != nil {
		return fmt.Errorf("receipt timestamp %q is not RFC3339: %w", r.Timestamp, err)
	}
	for _, f := range []struct {
		name string
		val  *float64
	}{
		{"score", r.Score},
		{"probability_yes", r.ProbabilityYes},
		{"provider_confidence", r.ProviderConfidence},
		{"instinct_margin", r.InstinctMargin},
	} {
		if f.val != nil && !instinct.IsFinite(*f.val) {
			return fmt.Errorf("receipt field %q is not a finite number", f.name)
		}
	}
	for k, v := range r.Probabilities {
		if !instinct.IsFinite(v) {
			return fmt.Errorf("receipt probability %q is not a finite number", k)
		}
	}
	return nil
}

// Parse reads one receipt from r and validates it.
func Parse(rd io.Reader) (DecisionReceipt, error) {
	var out DecisionReceipt
	dec := json.NewDecoder(rd)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("parsing receipt: %w", err)
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

// Load reads one receipt from a file.
func Load(path string) (DecisionReceipt, error) {
	// #nosec G304 -- path names a local receipt artifact chosen by the
	// operator, not untrusted network input.
	f, err := os.Open(path)
	if err != nil {
		return DecisionReceipt{}, fmt.Errorf("opening receipt: %w", err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}
