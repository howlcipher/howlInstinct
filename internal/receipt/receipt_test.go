package receipt

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

func fixedIDs() IDFunc {
	n := 0
	return func() string {
		n++
		return "dec_fixed_" + string(rune('a'+n-1))
	}
}

func sampleRequest() instinct.Request {
	return instinct.Request{
		State: "All payment requests are returning HTTP 500.",
		Questions: map[string]instinct.Question{
			"urgent": {Type: instinct.TypeNoul, Instructions: "Is this urgent?"},
			"category": {Type: instinct.TypeClassify, Instructions: "Category?",
				Options: []instinct.Option{{Name: "billing"}, {Name: "outage"}}},
		},
		CorrelationID: "corr-1",
	}
}

func sampleResponse() instinct.Response {
	return instinct.Response{
		Judgments: map[string]instinct.Judgment{
			"urgent": {
				QuestionID: "urgent", Type: instinct.TypeNoul,
				Outcome: instinct.OutcomeAcceptableConfidence,
				Bool:    instinct.Bool(true), ProbabilityYes: instinct.Float(0.97),
				InstinctMargin: instinct.Float(0.94),
			},
			"category": {
				QuestionID: "category", Type: instinct.TypeClassify,
				CompiledTo: instinct.TypeChoice,
				Outcome:    instinct.OutcomeAcceptableConfidence,
				Choice:     instinct.String("outage"),
				Probabilities: map[string]float64{
					"billing": 0.02, "outage": 0.98,
				},
				ProviderConfidence: instinct.Float(0.98),
				InstinctMargin:     instinct.Float(0.96),
			},
		},
		Provider: "mock", Model: "mock-1",
	}
}

func sampleMeta() Meta {
	return Meta{
		BatchID:   "batch-1",
		Timestamp: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
		Provider:  "mock",
		Model:     "mock-1",
		LatencyMS: 42,
	}
}

func TestBuildProducesOneReceiptPerQuestionInSortedOrder(t *testing.T) {
	got, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Build() produced %d receipts, want 2", len(got))
	}
	if got[0].QuestionID != "category" || got[1].QuestionID != "urgent" {
		t.Fatalf("receipts are not in sorted question order: %q, %q",
			got[0].QuestionID, got[1].QuestionID)
	}
	for _, r := range got {
		if err := r.Validate(); err != nil {
			t.Errorf("receipt %q failed validation: %v", r.QuestionID, err)
		}
		if r.BatchID != "batch-1" {
			t.Errorf("receipt %q has batch id %q, want the shared batch id", r.QuestionID, r.BatchID)
		}
	}
	if got[0].DecisionID == got[1].DecisionID {
		t.Error("two judgments share a decision id; each decision must be individually identifiable")
	}
}

// Classify is sugar over choice, but a receipt must still record what the
// caller actually asked for, or the audit trail describes a question nobody
// posed.
func TestBuildPreservesClassifyProvenance(t *testing.T) {
	got, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var cat DecisionReceipt
	for _, r := range got {
		if r.QuestionID == "category" {
			cat = r
		}
	}
	if cat.Type != instinct.TypeClassify {
		t.Errorf("decision_type = %q, want %q", cat.Type, instinct.TypeClassify)
	}
	if cat.CompiledTo != instinct.TypeChoice {
		t.Errorf("compiled_to = %q, want %q", cat.CompiledTo, instinct.TypeChoice)
	}
}

// A noul carries no provider confidence. The receipt must not invent one.
func TestBuildKeepsAbsentConfidenceAbsent(t *testing.T) {
	got, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, r := range got {
		if r.QuestionID != "urgent" {
			continue
		}
		if r.ProviderConfidence != nil {
			t.Fatalf("noul receipt carries provider_confidence = %v, want absent", *r.ProviderConfidence)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if strings.Contains(string(raw), "provider_confidence") {
			t.Fatalf("noul receipt serialized provider_confidence: %s", raw)
		}
	}
}

// State is the field most likely to contain logs, customer records, or
// secrets, and a receipt is durable. Retaining it must require asking.
func TestStateIsNotRetainedByDefault(t *testing.T) {
	req := sampleRequest()
	got, err := Build(req, sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, r := range got {
		if r.State != "" {
			t.Fatalf("receipt %q retained raw state without being asked: %q", r.QuestionID, r.State)
		}
		if r.StateHash == "" {
			t.Fatalf("receipt %q has no state hash, so the input is unidentifiable", r.QuestionID)
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "HTTP 500") {
			t.Fatalf("receipt %q leaked state content: %s", r.QuestionID, raw)
		}
	}
}

func TestStateIsRetainedOnExplicitOptIn(t *testing.T) {
	req := sampleRequest()
	req.RetainState = true
	got, err := Build(req, sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, r := range got {
		if r.State != req.State {
			t.Fatalf("receipt %q did not retain state on opt-in", r.QuestionID)
		}
	}
}

// The whole endpoint field is reconstructed from scheme, host, and path, so a
// credential cannot ride along in any other component.
func TestBuildStripsCredentialsFromEndpoint(t *testing.T) {
	meta := sampleMeta()
	meta.Endpoint = "https://bot:sk-live-SUPERSECRET@api.example.com/v1/systemone?api_key=sk-also-secret"

	got, err := Build(sampleRequest(), sampleResponse(), meta, fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, r := range got {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		for _, forbidden := range []string{"SUPERSECRET", "sk-live", "sk-also-secret", "api_key", "bot:"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("receipt leaked %q: %s", forbidden, raw)
			}
		}
		if r.ProviderEndpoint != "https://api.example.com/v1/systemone" {
			t.Fatalf("provider_endpoint = %q, want the credential-free form", r.ProviderEndpoint)
		}
	}
}

// A provider that fails to answer a question still has to leave a record that
// the question was asked, or the batch silently shrinks.
func TestBuildRecordsAMissingJudgment(t *testing.T) {
	resp := sampleResponse()
	delete(resp.Judgments, "urgent")

	got, err := Build(sampleRequest(), resp, sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Build() produced %d receipts, want 2 even with a missing judgment", len(got))
	}
	for _, r := range got {
		if r.QuestionID != "urgent" {
			continue
		}
		if r.Outcome != instinct.OutcomeUnavailable {
			t.Fatalf("outcome = %q, want %q", r.Outcome, instinct.OutcomeUnavailable)
		}
		if r.Error == "" {
			t.Fatal("a missing judgment produced no error text")
		}
	}
}

func TestReceiptValidate(t *testing.T) {
	valid := func() DecisionReceipt {
		r, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		return r[0]
	}

	tests := []struct {
		name    string
		mutate  func(*DecisionReceipt)
		wantErr string
	}{
		{"valid", func(*DecisionReceipt) {}, ""},
		{"wrong schema", func(r *DecisionReceipt) { r.Schema = "something.else/v1" }, "declares schema"},
		{"wrong version", func(r *DecisionReceipt) { r.ReceiptVersion = 99 }, "declares version"},
		{"missing decision id", func(r *DecisionReceipt) { r.DecisionID = "" }, "decision_id"},
		{"missing state hash", func(r *DecisionReceipt) { r.StateHash = "" }, "state_hash"},
		{"missing provider", func(r *DecisionReceipt) { r.Provider = "" }, "provider"},
		{"unknown type", func(r *DecisionReceipt) { r.Type = "oracle" }, "unknown decision_type"},
		{"unknown outcome", func(r *DecisionReceipt) { r.Outcome = "APPROVED" }, "unknown outcome"},
		{"bad timestamp", func(r *DecisionReceipt) { r.Timestamp = "yesterday" }, "not RFC3339"},
		{
			"escalation reason without the flag",
			func(r *DecisionReceipt) {
				r.EscalationReason = instinct.ReasonBelowCallerThreshold
				r.NeedsEscalation = false
			},
			"needs_escalation is false",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := valid()
			tc.mutate(&r)
			err := r.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A receipt must survive a write/read cycle unchanged, or it is not durable.
func TestReceiptRoundTripsThroughParse(t *testing.T) {
	built, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, want := range built {
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		got, err := Parse(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		again, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if string(raw) != string(again) {
			t.Fatalf("receipt did not round trip:\n  %s\n  %s", raw, again)
		}
	}
}

// Building the same decision twice must differ only in the volatile fields.
// Everything a caller would compare across runs has to be stable.
func TestReceiptHashesAreStableAcrossBuilds(t *testing.T) {
	first, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for i := 0; i < 25; i++ {
		again, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		for j := range first {
			if first[j].StateHash != again[j].StateHash {
				t.Fatalf("state hash varied across builds")
			}
			if first[j].QuestionHash != again[j].QuestionHash {
				t.Fatalf("question hash varied across builds for %q", first[j].QuestionID)
			}
		}
	}
}

func TestNewIDIsUniqueAndPrefixed(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := NewID()
		if !strings.HasPrefix(id, "dec_") {
			t.Fatalf("NewID() = %q, want a dec_ prefix", id)
		}
		if seen[id] {
			t.Fatalf("NewID() produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}
