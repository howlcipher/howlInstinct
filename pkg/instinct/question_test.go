package instinct

import (
	"strings"
	"testing"
)

func TestDecisionTypeIsValidAndLower(t *testing.T) {
	tests := []struct {
		name      string
		in        DecisionType
		wantValid bool
		wantLower DecisionType
	}{
		{"noul", TypeNoul, true, TypeNoul},
		{"choice", TypeChoice, true, TypeChoice},
		{"score", TypeScore, true, TypeScore},
		{"classify lowers to choice", TypeClassify, true, TypeChoice},
		{"unknown", DecisionType("oracle"), false, DecisionType("oracle")},
		{"empty", DecisionType(""), false, DecisionType("")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.IsValid(); got != tc.wantValid {
				t.Errorf("IsValid() = %v, want %v", got, tc.wantValid)
			}
			if got := tc.in.Lower(); got != tc.wantLower {
				t.Errorf("Lower() = %q, want %q", got, tc.wantLower)
			}
		})
	}
}

// Classify must never reach a provider as a fourth primitive. This asserts the
// lowering is total: every valid type lowers to something a provider speaks.
func TestLowerAlwaysYieldsAProviderPrimitive(t *testing.T) {
	providerPrimitives := map[DecisionType]bool{
		TypeNoul: true, TypeChoice: true, TypeScore: true,
	}
	for typ := range validDecisionTypes {
		if !providerPrimitives[typ.Lower()] {
			t.Errorf("%q lowers to %q, which is not a provider primitive", typ, typ.Lower())
		}
	}
}

func TestValidateID(t *testing.T) {
	lim := DefaultLimits()
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"simple", "urgent", false},
		{"with underscore", "is_incident", false},
		{"with dot and hyphen", "risk.level-2", false},
		{"digit first", "1st", false},
		{"empty", "", true},
		{"leading underscore", "_hidden", true},
		{"space", "is urgent", true},
		{"slash", "a/b", true},
		{"newline", "a\nb", true},
		{"too long", strings.Repeat("a", lim.MaxIDBytes+1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateID(tc.id, lim)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateID(%q) error = %v, wantErr %v", tc.id, err, tc.wantErr)
			}
			if err != nil {
				assertKind(t, err, KindInvalidInput)
			}
		})
	}
}

func TestQuestionValidate(t *testing.T) {
	lim := DefaultLimits()
	opts := []Option{{Name: "billing"}, {Name: "outage"}}
	levels := []Level{{Name: "low"}, {Name: "high"}}

	tests := []struct {
		name    string
		q       Question
		wantErr string
	}{
		{"valid noul", Question{Type: TypeNoul, Instructions: "Is this an outage?"}, ""},
		{"valid choice", Question{Type: TypeChoice, Instructions: "Category?", Options: opts}, ""},
		{"valid classify", Question{Type: TypeClassify, Instructions: "Category?", Options: opts}, ""},
		{"valid score", Question{Type: TypeScore, Instructions: "Severity?", Levels: levels}, ""},

		{"unknown type", Question{Type: "oracle", Instructions: "x"}, "unknown type"},
		{"empty instructions", Question{Type: TypeNoul}, "empty instructions"},
		{"noul with options", Question{Type: TypeNoul, Instructions: "x", Options: opts}, "supplies choice options"},
		{"noul with levels", Question{Type: TypeNoul, Instructions: "x", Levels: levels}, "supplies score levels"},
		{"choice with no options", Question{Type: TypeChoice, Instructions: "x"}, "supplies no options"},
		{"choice with levels", Question{Type: TypeChoice, Instructions: "x", Options: opts, Levels: levels}, "supplies score levels"},
		{"classify with no options", Question{Type: TypeClassify, Instructions: "x"}, "supplies no options"},
		{"score with options", Question{Type: TypeScore, Instructions: "x", Levels: levels, Options: opts}, "supplies choice options"},

		{
			"duplicate option names",
			Question{Type: TypeChoice, Instructions: "x", Options: []Option{{Name: "a"}, {Name: "a"}}},
			"repeats option name",
		},
		{
			"duplicate level names",
			Question{Type: TypeScore, Instructions: "x", Levels: []Level{{Name: "a"}, {Name: "a"}}},
			"repeats level name",
		},
		{
			"empty option name",
			Question{Type: TypeChoice, Instructions: "x", Options: []Option{{Name: ""}}},
			"empty name",
		},
		{
			"too few levels",
			Question{Type: TypeScore, Instructions: "x", Levels: []Level{{Name: "only"}}},
			"allowed range",
		},
		{
			"too many levels",
			Question{Type: TypeScore, Instructions: "x", Levels: manyLevels(lim.MaxLevels + 1)},
			"allowed range",
		},
		{
			"too many options",
			Question{Type: TypeChoice, Instructions: "x", Options: manyOptions(lim.MaxOptions + 1)},
			"limit is",
		},
		{
			"oversized instructions",
			Question{Type: TypeNoul, Instructions: strings.Repeat("x", lim.MaxInstructionBytes+1)},
			"limit is",
		},
		{
			"invalid utf8 instructions",
			Question{Type: TypeNoul, Instructions: "bad \xff byte"},
			"not valid UTF-8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate("qid", lim)
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
			assertKind(t, err, KindInvalidInput)
		})
	}
}

// Score levels are ordered and the index is the value, so the legend must
// come back in the order it was supplied, not in any normalized order.
func TestLevelNamesPreserveOrder(t *testing.T) {
	q := Question{Type: TypeScore, Levels: []Level{
		{Name: "negligible"}, {Name: "low"}, {Name: "moderate"}, {Name: "high"}, {Name: "critical"},
	}}
	want := []string{"negligible", "low", "moderate", "high", "critical"}
	got := q.LevelNames()
	if len(got) != len(want) {
		t.Fatalf("LevelNames() length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("LevelNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func manyOptions(n int) []Option {
	out := make([]Option, n)
	for i := range out {
		out[i] = Option{Name: string(rune('a'+i%26)) + itoa(i)}
	}
	return out
}

func manyLevels(n int) []Level {
	out := make([]Level, n)
	for i := range out {
		out[i] = Level{Name: "level" + itoa(i)}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	got, ok := KindOf(err)
	if !ok {
		t.Fatalf("error %v carries no kind, want %q", err, want)
	}
	if got != want {
		t.Fatalf("error kind = %q, want %q", got, want)
	}
}
