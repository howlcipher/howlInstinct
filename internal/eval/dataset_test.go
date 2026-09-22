package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howlcipher/howlinstinct/internal/decision"
	"github.com/howlcipher/howlinstinct/internal/provider/mock"
)

func writeDataset(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing dataset: %v", err)
	}
	return path
}

const goodDataset = `
schema: howlinstinct.eval_dataset/v1
name: demo
version: 1
question:
  type: classify
  instructions: Which category?
  options: [bug, feature]
cases:
  - id: c1
    state: "it crashes"
    expected: bug
  - id: c2
    state: "please add dark mode"
    expected: feature
`

func TestLoadAcceptsYAMLAndJSON(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		ds, err := Load(writeDataset(t, "cases.yaml", goodDataset))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(ds.Cases) != 2 {
			t.Fatalf("loaded %d cases, want 2", len(ds.Cases))
		}
		if !strings.HasPrefix(ds.SHA256, "sha256:") {
			t.Errorf("dataset digest = %q, want a labelled sha256", ds.SHA256)
		}
	})

	t.Run("json", func(t *testing.T) {
		body := `{"schema":"howlinstinct.eval_dataset/v1","name":"demo","version":1,
          "question":{"type":"noul","instructions":"Is it broken?"},
          "cases":[{"id":"c1","state":"it crashes","expected":true}]}`
		if _, err := Load(writeDataset(t, "cases.json", body)); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})

	t.Run("unsupported extension", func(t *testing.T) {
		if _, err := Load(writeDataset(t, "cases.txt", goodDataset)); err == nil {
			t.Fatal("Load() = nil, want an error for an unsupported extension")
		}
	})
}

// The digest identifies exactly which bytes were measured, so editing the
// dataset must change it. Otherwise a report could name a dataset whose
// content has since moved on.
func TestDatasetDigestTracksContent(t *testing.T) {
	a, err := Load(writeDataset(t, "cases.yaml", goodDataset))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	b, err := Load(writeDataset(t, "cases.yaml", goodDataset+"\n  - id: c3\n    state: x\n    expected: bug\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if a.SHA256 == b.SHA256 {
		t.Fatal("editing the dataset did not change its digest")
	}
}

func TestDatasetValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"wrong schema",
			strings.Replace(goodDataset, "howlinstinct.eval_dataset/v1", "something.else/v1", 1),
			"declares schema",
		},
		{
			"no cases",
			"schema: howlinstinct.eval_dataset/v1\nname: demo\nversion: 1\ncases: []\n",
			"no cases",
		},
		{
			"duplicate case id",
			strings.Replace(goodDataset, "id: c2", "id: c1", 1),
			"repeats case id",
		},
		{
			// An unlabelled case cannot be scored, so admitting it would
			// silently shrink the denominator of every metric.
			"missing expected label",
			"schema: howlinstinct.eval_dataset/v1\nname: d\nversion: 1\n" +
				"question:\n  type: noul\n  instructions: q\n" +
				"cases:\n  - id: c1\n    state: s\n",
			"no expected label",
		},
		{
			// A typo in a label is caught before any provider is called,
			// rather than becoming an unscorable case afterwards.
			"expected label is not one of the options",
			strings.Replace(goodDataset, "expected: bug", "expected: buhg", 1),
			"not one of the options",
		},
		{
			"expected label wrong type for a noul",
			"schema: howlinstinct.eval_dataset/v1\nname: d\nversion: 1\n" +
				"question:\n  type: noul\n  instructions: q\n" +
				"cases:\n  - id: c1\n    state: s\n    expected: maybe\n",
			"must be true or false",
		},
		{
			"score level index out of range",
			"schema: howlinstinct.eval_dataset/v1\nname: d\nversion: 1\n" +
				"question:\n  type: score\n  instructions: q\n  levels: [low, high]\n" +
				"cases:\n  - id: c1\n    state: s\n    expected: 5\n",
			"outside the 2 levels",
		},
		{
			"case with no question and no dataset default",
			"schema: howlinstinct.eval_dataset/v1\nname: d\nversion: 1\n" +
				"cases:\n  - id: c1\n    state: s\n    expected: true\n",
			"no question",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeDataset(t, "cases.yaml", tc.body))
			if err == nil {
				t.Fatalf("Load() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// Score labels may be written as a level name or as an index, because both
// are natural in a hand-written dataset and they must agree.
func TestScoreLabelsAcceptNameOrIndex(t *testing.T) {
	body := "schema: howlinstinct.eval_dataset/v1\nname: d\nversion: 1\n" +
		"question:\n  type: score\n  instructions: q\n  levels: [low, mid, high]\n" +
		"cases:\n" +
		"  - id: byname\n    state: s\n    expected: high\n" +
		"  - id: byindex\n    state: s\n    expected: 2\n"

	ds, err := Load(writeDataset(t, "cases.yaml", body))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var first, second expectation
	for i, c := range ds.Cases {
		q, exp, err := c.prepare(ds.Question)
		if err != nil {
			t.Fatalf("case %q: %v", c.ID, err)
		}
		if q.Type != "score" {
			t.Fatalf("case %q resolved to type %q", c.ID, q.Type)
		}
		if i == 0 {
			first = exp
		} else {
			second = exp
		}
	}
	if first != second {
		t.Fatalf("name %+v and index %+v did not resolve to the same label", first, second)
	}
}

// The suites shipped in the repository must stay loadable and runnable, or
// the documented commands stop working.
func TestShippedGoldenSuitesLoadAndRun(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "evals", "*", "cases.yaml"))
	if err != nil {
		t.Fatalf("globbing suites: %v", err)
	}
	if len(matches) < 4 {
		t.Fatalf("found %d shipped suites, want at least the four golden ones", len(matches))
	}

	engine := decision.New(mock.New())
	for _, path := range matches {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			ds, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%s) error = %v", path, err)
			}
			report, err := Run(context.Background(), ds, engine, Options{ProviderName: "mock"})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if report.Metrics.Scored != len(ds.Cases) {
				t.Errorf("scored %d of %d cases; failures: %+v",
					report.Metrics.Scored, len(ds.Cases), report.Failures)
			}
			if report.Dataset.SHA256 == "" {
				t.Error("report does not identify the dataset it measured")
			}
			// Every report must carry its caveat, because evaluation numbers
			// are routinely quoted away from their context.
			if report.Caveat == "" {
				t.Error("report carries no caveat")
			}
			if report.EscalationRuleSupplied {
				t.Error("report claims a caller rule was supplied when none was")
			}
		})
	}
}

// The harness must not invent a threshold, so with no rule supplied nothing
// may be marked for escalation however uncertain the provider was.
func TestRunEscalatesNothingWithoutACallerRule(t *testing.T) {
	ds, err := Load(writeDataset(t, "cases.yaml", goodDataset))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	report, err := Run(context.Background(), ds, decision.New(mock.New()), Options{ProviderName: "mock"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Metrics.EscalationRate != 0 {
		t.Fatalf("escalation rate = %v with no caller rule, want 0", report.Metrics.EscalationRate)
	}
}

func TestRunRecordsFailuresRatherThanDroppingCases(t *testing.T) {
	ds, err := Load(writeDataset(t, "cases.yaml", goodDataset))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	engine := decision.New(mock.NewWithBehavior(mock.Behavior{OmitJudgments: []string{"c1"}}))

	report, err := Run(context.Background(), ds, engine, Options{ProviderName: "mock"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Metrics.Errored != 1 {
		t.Fatalf("errored = %d, want 1", report.Metrics.Errored)
	}
	if len(report.Failures) != 1 || report.Failures[0].CaseID != "c1" {
		t.Fatalf("failures = %+v, want the dropped case recorded by id", report.Failures)
	}
	// The total must still reflect every case in the dataset, so a reader
	// can see that something went missing.
	if report.Metrics.Cases != 2 {
		t.Fatalf("cases = %d, want the full dataset size", report.Metrics.Cases)
	}
}

func TestRunStopsOnACancelledContext(t *testing.T) {
	ds, err := Load(writeDataset(t, "cases.yaml", goodDataset))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Run(ctx, ds, decision.New(mock.New()), Options{}); err == nil {
		t.Fatal("Run() = nil, want cancellation to stop the run")
	}
}
