// Package eval measures a provider against version-controlled cases.
//
// The harness exists because "is this decision class trustworthy enough for
// this use" is an empirical question, and the only honest way to answer it is
// to measure. Its central discipline is therefore negative: a metric is
// reported only when the data can actually support it, and is otherwise
// reported as unavailable with the reason. A Brier score computed over cases
// where half the providers returned no distribution is not a slightly worse
// Brier score, it is a fabrication.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/howlcipher/howlinstinct/internal/receipt"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// DatasetSchema identifies the evaluation dataset contract.
const DatasetSchema = "howlinstinct.eval_dataset/v1"

// Dataset is a version-controlled set of evaluation cases.
type Dataset struct {
	Schema      string `json:"schema" yaml:"schema"`
	Name        string `json:"name" yaml:"name"`
	Version     int    `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description"`

	// Question is the default question for every case. Suites usually ask
	// one question of many states, so repeating it per case would be noise
	// and an invitation for cases to drift apart.
	Question *Question `json:"question,omitempty" yaml:"question"`

	Cases []Case `json:"cases" yaml:"cases"`

	// Path and SHA256 are filled in by Load so a report can identify exactly
	// which bytes were measured.
	Path   string `json:"path,omitempty" yaml:"-"`
	SHA256 string `json:"sha256,omitempty" yaml:"-"`
}

// Question mirrors instinct.Question in a form convenient for datasets.
type Question struct {
	Type         string   `json:"type" yaml:"type"`
	Instructions string   `json:"instructions" yaml:"instructions"`
	Options      []string `json:"options,omitempty" yaml:"options"`
	Levels       []string `json:"levels,omitempty" yaml:"levels"`
	TrueMeaning  string   `json:"true_meaning,omitempty" yaml:"true_meaning"`
	FalseMeaning string   `json:"false_meaning,omitempty" yaml:"false_meaning"`
}

// Case is one labelled example.
type Case struct {
	ID    string `json:"id" yaml:"id"`
	State string `json:"state" yaml:"state"`

	// Question overrides the dataset-level question for this case.
	Question *Question `json:"question,omitempty" yaml:"question"`

	// Expected is the ground-truth label: a bool for noul, an option name
	// for choice and classify, and either a level name or a level index for
	// score.
	Expected any `json:"expected" yaml:"expected"`

	Notes string `json:"notes,omitempty" yaml:"notes"`
}

// Load reads a dataset from a YAML or JSON file and validates it.
func Load(path string) (Dataset, error) {
	// #nosec G304 -- the path names an evaluation dataset chosen by the
	// operator running the command.
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, instinct.Wrap(instinct.KindInvalidInput, "eval", err,
			"reading dataset")
	}

	var ds Dataset
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		if err := json.Unmarshal(raw, &ds); err != nil {
			return ds, instinct.Wrap(instinct.KindInvalidInput, "eval", err,
				"parsing dataset %s", path)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &ds); err != nil {
			return ds, instinct.Wrap(instinct.KindInvalidInput, "eval", err,
				"parsing dataset %s", path)
		}
	default:
		return ds, instinct.Errorf(instinct.KindInvalidInput, "eval",
			"dataset %s has unsupported extension %q; use .yaml, .yml, or .json", path, ext)
	}

	ds.Path = path
	// Hashing the bytes, not the parsed structure, is what lets a report
	// name exactly which dataset produced it even after the file is edited.
	ds.SHA256 = receipt.HashString(string(raw))

	if err := ds.Validate(); err != nil {
		return ds, err
	}
	return ds, nil
}

// Validate checks the dataset's self-consistency, starting with the schema
// identifier so that a document claiming to be something else is rejected
// before its fields are interpreted.
func (d Dataset) Validate() error {
	if d.Schema != DatasetSchema {
		return instinct.Errorf(instinct.KindInvalidInput, "eval",
			"dataset declares schema %q, want %q", d.Schema, DatasetSchema)
	}
	if d.Name == "" {
		return instinct.Errorf(instinct.KindInvalidInput, "eval", "dataset has no name")
	}
	if len(d.Cases) == 0 {
		return instinct.Errorf(instinct.KindInvalidInput, "eval", "dataset %s has no cases", d.Name)
	}

	seen := make(map[string]bool, len(d.Cases))
	for i, c := range d.Cases {
		if c.ID == "" {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"dataset %s case %d has no id", d.Name, i)
		}
		if seen[c.ID] {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"dataset %s repeats case id %q", d.Name, c.ID)
		}
		seen[c.ID] = true

		if c.State == "" {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"case %q has no state", c.ID)
		}
		q := c.resolveQuestion(d.Question)
		if q == nil {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"case %q has no question and the dataset supplies no default", c.ID)
		}
		question, err := q.toInstinct()
		if err != nil {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"case %q: %v", c.ID, err)
		}
		if err := question.Validate(c.ID, instinct.DefaultLimits()); err != nil {
			return err
		}
		if c.Expected == nil {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"case %q has no expected label; an unlabelled case cannot be scored", c.ID)
		}
		// Checking the label against the question now means a typo in a
		// dataset is caught before a provider is called, rather than turning
		// into a silently unscorable case afterwards.
		if _, err := normalizeExpected(question, c.Expected); err != nil {
			return instinct.Errorf(instinct.KindInvalidInput, "eval",
				"case %q: %v", c.ID, err)
		}
	}
	return nil
}

func (c Case) resolveQuestion(fallback *Question) *Question {
	if c.Question != nil {
		return c.Question
	}
	return fallback
}

// toInstinct converts a dataset question into the core representation.
func (q Question) toInstinct() (instinct.Question, error) {
	typ := instinct.DecisionType(q.Type)
	if !typ.IsValid() {
		return instinct.Question{}, fmt.Errorf("unknown question type %q", q.Type)
	}
	out := instinct.Question{
		Type:         typ,
		Instructions: q.Instructions,
		TrueMeaning:  q.TrueMeaning,
		FalseMeaning: q.FalseMeaning,
	}
	for _, name := range q.Options {
		out.Options = append(out.Options, instinct.Option{Name: name})
	}
	for _, name := range q.Levels {
		out.Levels = append(out.Levels, instinct.Level{Name: name})
	}
	return out, nil
}

// expectation is a ground-truth label reduced to a comparable form.
type expectation struct {
	// Label is the expected class: "true"/"false" for a noul, an option name
	// for a choice, a level name for a score.
	Label string
	// Index is the expected level index, for score questions only.
	Index int
}

// normalizeExpected reduces a case's label to a form that can be compared
// against a judgment, rejecting labels the question cannot produce.
func normalizeExpected(q instinct.Question, raw any) (expectation, error) {
	switch q.Type.Lower() {
	case instinct.TypeNoul:
		switch v := raw.(type) {
		case bool:
			return expectation{Label: boolLabel(v)}, nil
		case string:
			switch strings.ToLower(v) {
			case "true", "yes":
				return expectation{Label: "true"}, nil
			case "false", "no":
				return expectation{Label: "false"}, nil
			}
		}
		return expectation{}, fmt.Errorf("expected must be true or false for a noul, got %v", raw)

	case instinct.TypeChoice:
		name, ok := raw.(string)
		if !ok {
			return expectation{}, fmt.Errorf("expected must be an option name, got %v", raw)
		}
		for _, o := range q.Options {
			if o.Name == name {
				return expectation{Label: name}, nil
			}
		}
		return expectation{}, fmt.Errorf("expected %q is not one of the options %v", name, q.OptionNames())

	case instinct.TypeScore:
		levels := q.LevelNames()
		switch v := raw.(type) {
		case string:
			for i, name := range levels {
				if name == v {
					return expectation{Label: name, Index: i}, nil
				}
			}
			return expectation{}, fmt.Errorf("expected %q is not one of the levels %v", v, levels)
		case int:
			return indexExpectation(v, levels)
		case int64:
			return indexExpectation(int(v), levels)
		case float64:
			if v != float64(int(v)) {
				return expectation{}, fmt.Errorf("expected level index %v is not a whole number", v)
			}
			return indexExpectation(int(v), levels)
		}
		return expectation{}, fmt.Errorf("expected must be a level name or index, got %v", raw)
	}
	return expectation{}, fmt.Errorf("unsupported question type %q", q.Type)
}

func indexExpectation(i int, levels []string) (expectation, error) {
	if i < 0 || i >= len(levels) {
		return expectation{}, fmt.Errorf("expected level index %d is outside the %d levels defined", i, len(levels))
	}
	return expectation{Label: levels[i], Index: i}, nil
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
