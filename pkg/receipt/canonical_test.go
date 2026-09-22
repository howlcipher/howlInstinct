package receipt

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

func TestCanonicalizeSortsKeysAtEveryDepth(t *testing.T) {
	in := map[string]any{
		"b": 1,
		"a": []any{2, map[string]any{"z": 1, "y": 2}},
	}
	want := `{"a":[2,{"y":2,"z":1}],"b":1}`
	got, err := Canonicalize(in)
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("Canonicalize() = %s, want %s", got, want)
	}
}

// This is the property the receipt hashes depend on. Go randomizes map
// iteration order, so an encoder that merely walked the map would produce a
// different digest on different runs for the same logical input.
func TestCanonicalizeIsStableAcrossMapOrdering(t *testing.T) {
	build := func() map[string]any {
		m := map[string]any{}
		for _, k := range []string{"zulu", "alpha", "mike", "bravo", "yankee", "charlie", "delta"} {
			m[k] = map[string]any{"name": k, "n": len(k)}
		}
		return m
	}
	first, err := Canonicalize(build())
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	for i := 0; i < 200; i++ {
		got, err := Canonicalize(build())
		if err != nil {
			t.Fatalf("Canonicalize() error = %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("canonical form varied across runs:\n  %s\n  %s", first, got)
		}
	}
}

// The same request built by inserting questions in different orders is the
// same request, and must hash identically.
func TestHashCanonicalIgnoresInsertionOrder(t *testing.T) {
	a := map[string]instinct.Question{}
	a["urgent"] = instinct.Question{Type: instinct.TypeNoul, Instructions: "Is this urgent?"}
	a["severity"] = instinct.Question{Type: instinct.TypeScore, Instructions: "How severe?",
		Levels: []instinct.Level{{Name: "low"}, {Name: "high"}}}

	b := map[string]instinct.Question{}
	b["severity"] = instinct.Question{Type: instinct.TypeScore, Instructions: "How severe?",
		Levels: []instinct.Level{{Name: "low"}, {Name: "high"}}}
	b["urgent"] = instinct.Question{Type: instinct.TypeNoul, Instructions: "Is this urgent?"}

	ha, err := HashCanonical(a)
	if err != nil {
		t.Fatalf("HashCanonical(a) error = %v", err)
	}
	hb, err := HashCanonical(b)
	if err != nil {
		t.Fatalf("HashCanonical(b) error = %v", err)
	}
	if ha != hb {
		t.Fatalf("insertion order changed the digest:\n  %s\n  %s", ha, hb)
	}
}

// Ordered data must NOT be order-insensitive: score levels carry meaning in
// their order, so reordering them is a different question.
func TestHashCanonicalRespectsArrayOrder(t *testing.T) {
	up := instinct.Question{Type: instinct.TypeScore, Instructions: "Severity?",
		Levels: []instinct.Level{{Name: "low"}, {Name: "high"}}}
	down := instinct.Question{Type: instinct.TypeScore, Instructions: "Severity?",
		Levels: []instinct.Level{{Name: "high"}, {Name: "low"}}}

	hu, _ := HashCanonical(up)
	hd, _ := HashCanonical(down)
	if hu == hd {
		t.Fatal("reversing score levels did not change the digest, but level order is the scale")
	}
}

// Pinned digests. If these change, the receipt contract changed, and every
// previously stored receipt just became unverifiable. That should never
// happen silently.
func TestGoldenDigestsAreStable(t *testing.T) {
	tests := []struct {
		name string
		got  func(t *testing.T) string
		want string
	}{
		{
			name: "empty string",
			got:  func(*testing.T) string { return HashString("") },
			// The well-known SHA-256 of the empty input, which also proves
			// the labelling convention is not disturbing the digest.
			want: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "state",
			got: func(*testing.T) string {
				return HashString("All payment requests are returning HTTP 500.")
			},
			want: "sha256:c29e8555e5350031cb95e22574cb31d6b53ee3a640f69123554a066ac8492db9",
		},
		{
			name: "noul question",
			got: func(t *testing.T) string {
				h, err := HashCanonical(instinct.Question{
					Type: instinct.TypeNoul, Instructions: "Is this an outage?",
				})
				if err != nil {
					t.Fatalf("HashCanonical() error = %v", err)
				}
				return h
			},
			want: "sha256:0363c389d9d2a68faaec3ee23841a82b30e7587eef81ec70d75e2458d5c167ab",
		},
		{
			name: "choice question",
			got: func(t *testing.T) string {
				h, err := HashCanonical(instinct.Question{
					Type:         instinct.TypeChoice,
					Instructions: "Which category?",
					Options: []instinct.Option{
						{Name: "billing", Description: "money movement"},
						{Name: "outage", Description: "availability"},
					},
				})
				if err != nil {
					t.Fatalf("HashCanonical() error = %v", err)
				}
				return h
			},
			want: "sha256:0b2f561de62613f70421f4fa0ce94e9a8d1745e5e3ede787fa9884c827a10412",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got(t); got != tc.want {
				t.Fatalf("digest = %s, want %s\nthe receipt hashing contract has changed", got, tc.want)
			}
		})
	}
}

// NaN and the infinities have no JSON form. Encoding one would produce a
// digest no other implementation could reproduce, so they are refused.
func TestCanonicalizeRejectsNonFiniteNumbers(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  float64
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Canonicalize(map[string]any{"p": tc.val}); err == nil {
				t.Fatal("Canonicalize() = nil error, want a rejection")
			}
			if _, err := HashCanonical(map[string]any{"p": tc.val}); err == nil {
				t.Fatal("HashCanonical() = nil error, want a rejection")
			}
		})
	}
}

// Canonical output must still be valid JSON that round-trips to equal data.
func TestCanonicalOutputRoundTrips(t *testing.T) {
	in := instinct.Judgment{
		QuestionID:         "severity",
		Type:               instinct.TypeScore,
		Outcome:            instinct.OutcomeAcceptableConfidence,
		Score:              instinct.Float(2.5),
		Legend:             []string{"low", "moderate", "high"},
		Probabilities:      map[string]float64{"low": 0.1, "moderate": 0.4, "high": 0.5},
		ProviderConfidence: instinct.Float(0.87),
	}
	canon, err := Canonicalize(in)
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	var back instinct.Judgment
	if err := json.Unmarshal(canon, &back); err != nil {
		t.Fatalf("canonical output is not valid JSON: %v", err)
	}
	if back.Score == nil || *back.Score != 2.5 {
		t.Fatalf("score did not round trip: %v", back.Score)
	}
	if strings.Contains(string(canon), " ") {
		t.Fatalf("canonical form contains insignificant whitespace: %s", canon)
	}
}

func TestSafeEndpointStripsCredentials(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "https://api.example.com/v1/systemone", "https://api.example.com/v1/systemone"},
		{"userinfo", "https://user:sk-secret@api.example.com/v1/systemone", "https://api.example.com/v1/systemone"},
		{"token in query", "https://api.example.com/v1/systemone?api_key=sk-secret", "https://api.example.com/v1/systemone"},
		{"fragment", "https://api.example.com/v1/x#sk-secret", "https://api.example.com/v1/x"},
		{"userinfo and query", "https://u:p@api.example.com/v1?k=sk-secret", "https://api.example.com/v1"},
		{"localhost with port", "http://127.0.0.1:8080/v1/systemone", "http://127.0.0.1:8080/v1/systemone"},
		{"empty", "", ""},
		{"unparseable yields nothing rather than echoing back", "://not a url", ""},
		{"no host", "file:///etc/passwd", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeEndpoint(tc.in)
			if got != tc.want {
				t.Fatalf("SafeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "secret") || strings.Contains(got, "sk-") {
				t.Fatalf("SafeEndpoint(%q) leaked a credential: %q", tc.in, got)
			}
		})
	}
}
