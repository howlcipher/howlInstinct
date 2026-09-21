package instinct

import (
	"math"
	"strings"
	"testing"
)

const validRequestJSON = `{
  "state": "All payment requests are returning HTTP 500.",
  "questions": {
    "urgent":   {"type": "noul",   "instructions": "Is this urgent?"},
    "category": {"type": "choice", "instructions": "Category?",
                 "options": [{"name": "billing"}, {"name": "outage"}]},
    "severity": {"type": "score",  "instructions": "Severity?",
                 "levels": [{"name": "low"}, {"name": "high"}]}
  }
}`

func TestDecodeRequestAcceptsAValidBatch(t *testing.T) {
	req, err := DecodeRequest(strings.NewReader(validRequestJSON), DefaultLimits())
	if err != nil {
		t.Fatalf("DecodeRequest() error = %v", err)
	}
	if len(req.Questions) != 3 {
		t.Fatalf("decoded %d questions, want 3", len(req.Questions))
	}
	ids := req.QuestionIDs()
	want := []string{"category", "severity", "urgent"} // sorted, for determinism
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("QuestionIDs() = %v, want %v", ids, want)
		}
	}
}

// encoding/json keeps the last value for a repeated key, so without an
// explicit check these two questions would silently collapse into one and the
// caller would get one answer where it asked for two, with no error.
func TestDecodeRequestRejectsDuplicateQuestionIDs(t *testing.T) {
	const dup = `{
      "state": "x",
      "questions": {
        "risk": {"type": "noul", "instructions": "first"},
        "risk": {"type": "noul", "instructions": "second"}
      }
    }`
	_, err := DecodeRequest(strings.NewReader(dup), DefaultLimits())
	if err == nil {
		t.Fatal("DecodeRequest() = nil, want an error for a duplicate question id")
	}
	if !strings.Contains(err.Error(), "duplicate key") || !strings.Contains(err.Error(), "risk") {
		t.Fatalf("error = %q, want it to name the duplicate key", err)
	}
	assertKind(t, err, KindInvalidInput)
}

func TestDecodeRequestRejectsDuplicateKeysAnywhere(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{
			"duplicate top level key",
			`{"state":"a","state":"b","questions":{"q":{"type":"noul","instructions":"i"}}}`,
		},
		{
			"duplicate key inside a question",
			`{"state":"a","questions":{"q":{"type":"noul","instructions":"i","instructions":"j"}}}`,
		},
		{
			"duplicate key inside an array element",
			`{"state":"a","questions":{"q":{"type":"choice","instructions":"i",
              "options":[{"name":"a","name":"b"}]}}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeRequest(strings.NewReader(tc.doc), DefaultLimits())
			if err == nil {
				t.Fatal("DecodeRequest() = nil, want a duplicate-key error")
			}
			if !strings.Contains(err.Error(), "duplicate key") {
				t.Fatalf("error = %q, want a duplicate-key error", err)
			}
		})
	}
}

func TestDecodeRequestRejectsMalformedDocuments(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"empty", "", "empty"},
		{"whitespace only", "   \n  ", "empty"},
		{"not json", "this is not json", "parsing request"},
		{"truncated", `{"state":"a","questions":{`, "parsing request"},
		{
			"unknown field",
			`{"state":"a","questions":{"q":{"type":"noul","instructions":"i"}},"retian_state":true}`,
			"parsing request",
		},
		{
			"trailing content",
			`{"state":"a","questions":{"q":{"type":"noul","instructions":"i"}}} {"extra":1}`,
			"trailing content",
		},
		{
			"no questions",
			`{"state":"a","questions":{}}`,
			"no questions",
		},
		{
			"invalid question id",
			`{"state":"a","questions":{"bad id":{"type":"noul","instructions":"i"}}}`,
			"question id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeRequest(strings.NewReader(tc.doc), DefaultLimits())
			if err == nil {
				t.Fatalf("DecodeRequest() = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
			assertKind(t, err, KindInvalidInput)
		})
	}
}

// The document bound must hold even when the attacker never closes the JSON,
// so that a hostile or broken caller cannot make us buffer without limit.
func TestDecodeRequestBoundsDocumentSize(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxStateBytes = 1024
	huge := `{"state":"` + strings.Repeat("a", 512*1024) + `"`
	_, err := DecodeRequest(strings.NewReader(huge), lim)
	if err == nil {
		t.Fatal("DecodeRequest() = nil, want a size error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %q, want a size error", err)
	}
}

func TestRequestValidate(t *testing.T) {
	lim := DefaultLimits()
	good := Question{Type: TypeNoul, Instructions: "Is this urgent?"}

	tests := []struct {
		name string
		req  Request
		want string
	}{
		{
			"valid",
			Request{State: "s", Questions: map[string]Question{"q": good}},
			"",
		},
		{
			"empty state",
			Request{Questions: map[string]Question{"q": good}},
			"state is empty",
		},
		{
			"invalid utf8 state",
			Request{State: "bad \xff", Questions: map[string]Question{"q": good}},
			"not valid UTF-8",
		},
		{
			"too many questions",
			Request{State: "s", Questions: tooManyQuestions(lim.MaxQuestions + 1)},
			"limit is",
		},
		{
			// A misspelled escalation key would silently disable the caller's
			// own gate, so it is an error rather than an ignored extra.
			"escalation names unknown question",
			Request{
				State:      "s",
				Questions:  map[string]Question{"q": good},
				Escalation: map[string]EscalationRule{"typo": {MinInstinctMargin: Float(0.5)}},
			},
			"unknown question",
		},
		{
			"escalation threshold out of range",
			Request{
				State:      "s",
				Questions:  map[string]Question{"q": good},
				Escalation: map[string]EscalationRule{"q": {MinInstinctMargin: Float(1.5)}},
			},
			"outside [0,1]",
		},
		{
			"escalation threshold NaN",
			Request{
				State:      "s",
				Questions:  map[string]Question{"q": good},
				Escalation: map[string]EscalationRule{"q": {MinProviderConfidence: Float(math.NaN())}},
			},
			"non-finite",
		},
		{
			"invalid correlation id",
			Request{State: "s", Questions: map[string]Question{"q": good}, CorrelationID: "has space"},
			"correlation_id is invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate(lim)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %q, want it to contain %q", err, tc.want)
			}
			assertKind(t, err, KindInvalidInput)
		})
	}
}

// Validation must not depend on Go's randomized map iteration order: the same
// invalid request has to produce the same error every time.
func TestRequestValidateIsDeterministic(t *testing.T) {
	req := Request{
		State: "s",
		Questions: map[string]Question{
			"zulu":    {Type: TypeChoice, Instructions: "x"},
			"alpha":   {Type: TypeChoice, Instructions: "x"},
			"mike":    {Type: TypeChoice, Instructions: "x"},
			"bravo":   {Type: TypeChoice, Instructions: "x"},
			"yankee":  {Type: TypeChoice, Instructions: "x"},
			"charlie": {Type: TypeChoice, Instructions: "x"},
		},
	}
	first := req.Validate(DefaultLimits()).Error()
	for i := 0; i < 50; i++ {
		if got := req.Validate(DefaultLimits()).Error(); got != first {
			t.Fatalf("validation error varied across runs:\n  %q\n  %q", first, got)
		}
	}
	if !strings.Contains(first, "alpha") {
		t.Fatalf("expected the lowest-sorted id to be reported first, got %q", first)
	}
}

func tooManyQuestions(n int) map[string]Question {
	out := make(map[string]Question, n)
	for i := 0; i < n; i++ {
		out["q"+itoa(i)] = Question{Type: TypeNoul, Instructions: "x"}
	}
	return out
}
