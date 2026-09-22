package jev

import (
	"encoding/json"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// The types in this file are the only place HowlInstinct knows what the
// System One wire format looks like. They mirror the upstream contract
// exactly, including its asymmetries, rather than being tidied up: the
// adapter's job is to speak the protocol that exists.

type wireRequest struct {
	State     string                  `json:"state"`
	Model     string                  `json:"model,omitempty"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`

	// Criteria is deliberately typed as any, because the contract gives it a
	// different shape per question type: an object of meanings for noul, an
	// object of option descriptions for choice, and an ordered array of level
	// names for score. Modelling that as three fields would not match the
	// protocol.
	Criteria any `json:"criteria,omitempty"`
}

type wireResponse struct {
	Model string `json:"model"`
	// Answers are held as raw messages so each one can be decoded against the
	// shape its question actually called for, and so an unknown-field check
	// can be applied per answer.
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   *wireUsage                 `json:"usage,omitempty"`
}

type wireUsage struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
}

// wireAnswer is the union of every answer shape. Every field is optional
// because which ones are present depends on the question type, and because a
// provider that omits one must be detected rather than defaulted.
type wireAnswer struct {
	// Noul is P(yes). The contract defines no confidence for a noul: the
	// probability is itself the uncertainty signal.
	Noul *float64 `json:"noul,omitempty"`

	Choice *string  `json:"choice,omitempty"`
	Score  *float64 `json:"score,omitempty"`
	Legend []string `json:"legend,omitempty"`

	// Probabilities is an object keyed by option name for choice, and an
	// array aligned with legend for score.
	Probabilities json.RawMessage `json:"probabilities,omitempty"`

	// Confidence is reported for choice and score. Some implementations also
	// emit it for noul even though the contract does not define one there; it
	// is preserved if present and never invented if absent.
	Confidence *float64 `json:"confidence,omitempty"`
}

// wireError is the upstream error envelope.
type wireError struct {
	Detail struct {
		ErrorType string `json:"error_type"`
		Message   string `json:"message"`
	} `json:"detail"`
}

// buildRequest translates a HowlInstinct request into the wire format.
//
// State travels in its own field and is never concatenated into a question's
// instructions. That separation is the adapter's structural defence against
// state-borne prompt injection: hostile text in the state is still only ever
// presented to the provider as the material being judged, never as part of
// the question being asked.
func buildRequest(req instinct.Request, model string) wireRequest {
	out := wireRequest{
		State:     req.State,
		Model:     model,
		Questions: make(map[string]wireQuestion, len(req.Questions)),
	}
	for id, q := range req.Questions {
		wq := wireQuestion{
			// Lower defensively. The engine lowers classify before calling a
			// provider, but an adapter used directly must not emit a type the
			// endpoint has never heard of.
			Type:         string(q.Type.Lower()),
			Instructions: q.Instructions,
		}
		switch q.Type.Lower() {
		case instinct.TypeNoul:
			if q.TrueMeaning != "" || q.FalseMeaning != "" {
				wq.Criteria = map[string]string{
					"true":  q.TrueMeaning,
					"false": q.FalseMeaning,
				}
			}
		case instinct.TypeChoice:
			criteria := make(map[string]any, len(q.Options))
			for _, o := range q.Options {
				if o.Description == "" {
					criteria[o.Name] = nil
					continue
				}
				criteria[o.Name] = o.Description
			}
			wq.Criteria = criteria
		case instinct.TypeScore:
			// Order is the scale, so this must stay an ordered array.
			wq.Criteria = q.LevelNames()
		}
		out.Questions[id] = wq
	}
	return out
}
