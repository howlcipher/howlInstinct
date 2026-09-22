package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// batchRequest is the three-primitive batch used throughout these tests.
func batchRequest() instinct.Request {
	return instinct.Request{
		State: "All payment requests are returning HTTP 500.",
		Questions: map[string]instinct.Question{
			"urgent": {
				Type: instinct.TypeNoul, Instructions: "Is this urgent?",
				TrueMeaning: "needs attention now", FalseMeaning: "can wait",
			},
			"category": {
				Type: instinct.TypeChoice, Instructions: "Which category?",
				Options: []instinct.Option{
					{Name: "billing", Description: "money movement"},
					{Name: "outage"},
				},
			},
			"severity": {
				Type: instinct.TypeScore, Instructions: "How severe?",
				Levels: []instinct.Level{{Name: "low"}, {Name: "moderate"}, {Name: "high"}},
			},
		},
	}
}

const goodAnswers = `{
  "model": "jev-test",
  "answers": {
    "urgent":   {"noul": 0.97},
    "category": {"choice": "outage", "probabilities": {"billing": 0.04, "outage": 0.96}, "confidence": 0.96},
    "severity": {"score": 1.7, "legend": ["low","moderate","high"],
                 "probabilities": [0.05, 0.2, 0.75], "confidence": 0.75}
  },
  "usage": {"input_tokens": 120, "output_tokens": 0}
}`

// serve starts a test server and returns a provider wired to it with
// instantaneous backoff, so retry logic is exercised without real sleeping.
func serve(t *testing.T, handler http.HandlerFunc, tweak ...func(*Config)) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Model = "jev-test"
	cfg.HTTPClient = srv.Client()
	for _, f := range tweak {
		f(&cfg)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	p.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return p, srv
}

func respondOK(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

func TestDecideHappyPathAcrossAllThreePrimitives(t *testing.T) {
	var seen wireRequest
	p, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Errorf("server could not decode the request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, goodAnswers)
	})

	resp, err := p.Decide(context.Background(), batchRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	// State must travel in its own field, never folded into instructions.
	// That separation is the structural defence against state-borne
	// injection, so it is asserted rather than assumed.
	if seen.State != batchRequest().State {
		t.Errorf("state = %q, want it carried verbatim in its own field", seen.State)
	}
	for id, q := range seen.Questions {
		if strings.Contains(q.Instructions, "HTTP 500") {
			t.Errorf("question %q had state concatenated into its instructions: %q", id, q.Instructions)
		}
	}

	// Criteria shapes must match the contract's asymmetry.
	if got := seen.Questions["severity"].Criteria; fmt.Sprint(got) != "[low moderate high]" {
		t.Errorf("score criteria = %v, want an ordered array of level names", got)
	}
	if _, ok := seen.Questions["category"].Criteria.(map[string]any); !ok {
		t.Errorf("choice criteria = %T, want an object keyed by option name", seen.Questions["category"].Criteria)
	}

	urgent := resp.Judgments["urgent"]
	if urgent.ProbabilityYes == nil || *urgent.ProbabilityYes != 0.97 {
		t.Errorf("noul probability = %v, want 0.97", urgent.ProbabilityYes)
	}
	if urgent.Bool == nil || !*urgent.Bool {
		t.Errorf("noul bool = %v, want true for P(yes)=0.97", urgent.Bool)
	}
	// The contract defines no confidence for a noul, and none was sent.
	if urgent.ProviderConfidence != nil {
		t.Errorf("noul carried provider confidence %v; none was reported", *urgent.ProviderConfidence)
	}

	cat := resp.Judgments["category"]
	if cat.Choice == nil || *cat.Choice != "outage" {
		t.Errorf("choice = %v, want outage", cat.Choice)
	}
	if cat.ProviderConfidence == nil || *cat.ProviderConfidence != 0.96 {
		t.Errorf("choice confidence = %v, want 0.96", cat.ProviderConfidence)
	}

	sev := resp.Judgments["severity"]
	if sev.Score == nil || *sev.Score != 1.7 {
		t.Errorf("score = %v, want 1.7", sev.Score)
	}
	if got := sev.Probabilities["high"]; got != 0.75 {
		t.Errorf("score probability for high = %v, want 0.75; the array must align with the legend", got)
	}
	if resp.Usage == nil || resp.Usage.InputTokens == nil || *resp.Usage.InputTokens != 120 {
		t.Errorf("usage was not carried through: %+v", resp.Usage)
	}
}

// Every case here is a provider answering something that cannot be trusted.
// None of them may produce a judgment.
func TestDecideRejectsUntrustworthyResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"answers a question that was never asked",
			`{"answers":{"urgent":{"noul":0.5},"category":{"choice":"outage"},
              "severity":{"score":1},"smuggled":{"noul":0.9}}}`,
			"never asked",
		},
		{
			"silently drops part of the batch",
			`{"answers":{"urgent":{"noul":0.5}}}`,
			"did not answer",
		},
		{
			"answers a noul with a choice",
			`{"answers":{"urgent":{"choice":"outage"},"category":{"choice":"outage"},"severity":{"score":1}}}`,
			"carries no noul probability",
		},
		{
			"mixes fields from two question types",
			`{"answers":{"urgent":{"noul":0.5,"score":2},"category":{"choice":"outage"},"severity":{"score":1}}}`,
			"another question type",
		},
		{
			"noul probability out of range",
			`{"answers":{"urgent":{"noul":1.4},"category":{"choice":"outage"},"severity":{"score":1}}}`,
			"outside [0,1]",
		},
		{
			"chooses an option that does not exist",
			`{"answers":{"urgent":{"noul":0.5},"category":{"choice":"cosmic_rays"},"severity":{"score":1}}}`,
			"not one of its options",
		},
		{
			"distribution does not sum to one",
			`{"answers":{"urgent":{"noul":0.5},
              "category":{"choice":"outage","probabilities":{"billing":0.1,"outage":0.2}},
              "severity":{"score":1}}}`,
			"summing to",
		},
		{
			"distribution names an option that does not exist",
			`{"answers":{"urgent":{"noul":0.5},
              "category":{"choice":"outage","probabilities":{"billing":0.1,"outage":0.8,"ghost":0.1}},
              "severity":{"score":1}}}`,
			"options that do not exist",
		},
		{
			"distribution omits an option",
			`{"answers":{"urgent":{"noul":0.5},
              "category":{"choice":"outage","probabilities":{"outage":1.0}},
              "severity":{"score":1}}}`,
			"no probability for options",
		},
		{
			"score outside the scale its levels define",
			`{"answers":{"urgent":{"noul":0.5},"category":{"choice":"outage"},"severity":{"score":7}}}`,
			"outside the scale",
		},
		{
			"legend does not match the levels asked",
			`{"answers":{"urgent":{"noul":0.5},"category":{"choice":"outage"},
              "severity":{"score":1,"legend":["low","high"]}}}`,
			"legend of 2 entries for 3 levels",
		},
		{
			"legend is reordered, which would silently change the scale",
			`{"answers":{"urgent":{"noul":0.5},"category":{"choice":"outage"},
              "severity":{"score":1,"legend":["high","moderate","low"]}}}`,
			"legend entry 0",
		},
		{
			"score probabilities do not align with the levels",
			`{"answers":{"urgent":{"noul":0.5},"category":{"choice":"outage"},
              "severity":{"score":1,"probabilities":[0.5,0.5]}}}`,
			"2 probabilities for 3 levels",
		},
		{
			"confidence out of range",
			`{"answers":{"urgent":{"noul":0.5},
              "category":{"choice":"outage","confidence":1.7},"severity":{"score":1}}}`,
			"confidence",
		},
		{
			"not json at all",
			`<html>502 Bad Gateway</html>`,
			"decoding response envelope",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := serve(t, respondOK(tc.body))
			_, err := p.Decide(context.Background(), batchRequest())
			if err == nil {
				t.Fatalf("Decide() = nil, want rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
			// An untrustworthy answer is an integrity problem, not an
			// availability one, and must be classified separately so it can
			// raise a different alarm.
			if kind, _ := instinct.KindOf(err); kind != instinct.KindProviderResponseInvalid {
				t.Fatalf("error kind = %q, want %q", kind, instinct.KindProviderResponseInvalid)
			}
		})
	}
}

// NaN and Inf are not JSON numbers, so a provider emitting them is already
// malformed. A provider emitting them as bare tokens must not crash us.
func TestDecideRejectsNonFiniteNumbers(t *testing.T) {
	p, _ := serve(t, respondOK(
		`{"answers":{"urgent":{"noul":1e999},"category":{"choice":"outage"},"severity":{"score":1}}}`))
	_, err := p.Decide(context.Background(), batchRequest())
	if err == nil {
		t.Fatal("Decide() = nil, want a rejection of a non-finite probability")
	}
}

func TestStrictSchemaRejectsDriftAndCanBeRelaxed(t *testing.T) {
	body := `{"answers":{"urgent":{"noul":0.5,"reasoning":"because"},
              "category":{"choice":"outage"},"severity":{"score":1}}}`

	t.Run("strict by default", func(t *testing.T) {
		p, _ := serve(t, respondOK(body))
		if _, err := p.Decide(context.Background(), batchRequest()); err == nil {
			t.Fatal("Decide() = nil, want an unknown field to be rejected")
		}
	})

	t.Run("relaxed by configuration", func(t *testing.T) {
		p, _ := serve(t, respondOK(body), func(c *Config) { c.StrictSchema = false })
		if _, err := p.Decide(context.Background(), batchRequest()); err != nil {
			t.Fatalf("Decide() error = %v, want the unknown field tolerated", err)
		}
	})
}

// A provider must not be able to exhaust memory by replying with an
// arbitrarily large body.
func TestResponseBodyIsBounded(t *testing.T) {
	p, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"answers":{"x":"`)
		for i := 0; i < 200; i++ {
			_, _ = io.WriteString(w, strings.Repeat("a", 1024))
		}
	}, func(c *Config) { c.MaxResponseBytes = 8 * 1024 })

	_, err := p.Decide(context.Background(), batchRequest())
	if err == nil {
		t.Fatal("Decide() = nil, want the oversized body to be refused")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %q, want a size limit error", err)
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantKind instinct.ErrorKind
	}{
		{"unauthorized is the operator's problem", 401, instinct.KindConfiguration},
		{"forbidden is the operator's problem", 403, instinct.KindConfiguration},
		{"bad request is the caller's problem", 400, instinct.KindInvalidInput},
		{"unprocessable is the caller's problem", 422, instinct.KindInvalidInput},
		{"rate limited", 429, instinct.KindProviderUnavailable},
		{"overloaded", 529, instinct.KindProviderUnavailable},
		{"service unavailable", 503, instinct.KindProviderUnavailable},
		{"internal error", 500, instinct.KindProviderUnavailable},
		{"teapot", 418, instinct.KindProviderUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"detail":{"error_type":"oops","message":"no"}}`)
			}, func(c *Config) { c.MaxRetries = 0 })

			_, err := p.Decide(context.Background(), batchRequest())
			if err == nil {
				t.Fatalf("Decide() = nil, want an error for HTTP %d", tc.status)
			}
			if kind, _ := instinct.KindOf(err); kind != tc.wantKind {
				t.Fatalf("HTTP %d mapped to kind %q, want %q", tc.status, kind, tc.wantKind)
			}
		})
	}
}

// Retrying must be reserved for failures that could plausibly succeed on a
// second attempt. Anything else multiplies load for no benefit.
func TestRetryPolicy(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantAttempts int32
	}{
		{"rate limiting is retried", 429, 3},
		{"overload is retried", 529, 3},
		{"gateway errors are retried", 502, 3},
		{"an unqualified internal error is not retried", 500, 1},
		{"a rejected request is never retried", 400, 1},
		{"a bad credential is never retried", 401, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var attempts int32
			p, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&attempts, 1)
				w.WriteHeader(tc.status)
			}, func(c *Config) { c.MaxRetries = 2 })

			_, _ = p.Decide(context.Background(), batchRequest())
			if got := atomic.LoadInt32(&attempts); got != tc.wantAttempts {
				t.Fatalf("provider was called %d times for HTTP %d, want %d", got, tc.status, tc.wantAttempts)
			}
		})
	}
}

func TestRetrySucceedsAfterATransientFailure(t *testing.T) {
	var attempts int32
	p, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, goodAnswers)
	})

	resp, err := p.Decide(context.Background(), batchRequest())
	if err != nil {
		t.Fatalf("Decide() error = %v, want the retry to succeed", err)
	}
	if len(resp.Judgments) != 3 {
		t.Fatalf("got %d judgments after retry, want 3", len(resp.Judgments))
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("provider was called %d times, want exactly 2", got)
	}
}

// A provider must never be able to hold a caller open indefinitely, and
// retries must not outlive the caller's own budget.
func TestDeadlineIsEnforcedAndBoundsRetries(t *testing.T) {
	var attempts int32
	p, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}, func(c *Config) { c.MaxRetries = 5 })
	// Restore real sleeping so an expired context actually stops the loop.
	p.sleep = sleepCtx

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Decide(ctx, batchRequest())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Decide() = nil, want a timeout")
	}
	if kind, _ := instinct.KindOf(err); kind != instinct.KindTimeout {
		t.Fatalf("error kind = %q, want %q", kind, instinct.KindTimeout)
	}
	if elapsed > time.Second {
		t.Fatalf("Decide() took %v; the caller's deadline did not bound the retry loop", elapsed)
	}
}

func TestCredentialIsSentAsAHeaderAndNeverInTheURL(t *testing.T) {
	const key = "sk-live-SUPERSECRET"
	t.Setenv("HOWLINSTINCT_TEST_KEY", key)

	var gotAuth, gotURL string
	p, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, goodAnswers)
	}, func(c *Config) { c.APIKeyEnv = "HOWLINSTINCT_TEST_KEY" })

	if _, err := p.Decide(context.Background(), batchRequest()); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if gotAuth != "Bearer "+key {
		t.Fatalf("Authorization header = %q, want the bearer credential", gotAuth)
	}
	if strings.Contains(gotURL, key) {
		t.Fatalf("the credential appeared in the request URL: %q", gotURL)
	}
	// And the endpoint recorded for provenance must be credential-free.
	if strings.Contains(p.Endpoint(), key) {
		t.Fatalf("Endpoint() leaked the credential: %q", p.Endpoint())
	}
}

// Configuration is printable in full, in a log or a bug report, without ever
// printing the key.
func TestDescribeNamesTheVariableAndNeverTheValue(t *testing.T) {
	const key = "sk-live-SUPERSECRET"
	t.Setenv("HOWLINSTINCT_TEST_KEY", key)

	cfg := DefaultConfig()
	cfg.BaseURL = "https://bot:" + key + "@api.example.com?token=" + key
	cfg.APIKeyEnv = "HOWLINSTINCT_TEST_KEY"

	got := cfg.Describe()
	if strings.Contains(got, key) {
		t.Fatalf("Describe() leaked the credential: %s", got)
	}
	if !strings.Contains(got, "$HOWLINSTINCT_TEST_KEY") {
		t.Fatalf("Describe() = %s, want it to name the environment variable", got)
	}
}

func TestMissingCredentialIsAConfigurationError(t *testing.T) {
	p, _ := serve(t, respondOK(goodAnswers), func(c *Config) {
		c.APIKeyEnv = "HOWLINSTINCT_DEFINITELY_UNSET_KEY"
	})
	_, err := p.Decide(context.Background(), batchRequest())
	if err == nil {
		t.Fatal("Decide() = nil, want a configuration error for the unset credential")
	}
	if kind, _ := instinct.KindOf(err); kind != instinct.KindConfiguration {
		t.Fatalf("error kind = %q, want %q", kind, instinct.KindConfiguration)
	}
	if !strings.Contains(err.Error(), "HOWLINSTINCT_DEFINITELY_UNSET_KEY") {
		t.Fatalf("error = %q, want it to name the variable the operator must set", err)
	}
}

func TestNewRejectsUnusableEndpoints(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no scheme", "api.example.com"},
		{"unsupported scheme", "ftp://api.example.com"},
		{"no host", "https://"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BaseURL = tc.url
			_, err := New(cfg)
			if err == nil {
				t.Fatalf("New(%q) = nil, want a configuration error", tc.url)
			}
			if kind, _ := instinct.KindOf(err); kind != instinct.KindConfiguration {
				t.Fatalf("error kind = %q, want %q", kind, instinct.KindConfiguration)
			}
		})
	}
}

// Provider error text reaches logs and terminals, so control characters that
// could forge log lines or drive escape sequences must not survive.
func TestProviderErrorTextIsSanitized(t *testing.T) {
	p, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w,
			"{\"detail\":{\"error_type\":\"x\",\"message\":\"line1\\nFAKE LOG\\u001b[31m\"}}")
	}, func(c *Config) { c.MaxRetries = 0 })

	_, err := p.Decide(context.Background(), batchRequest())
	if err == nil {
		t.Fatal("Decide() = nil, want an error")
	}
	if strings.ContainsAny(err.Error(), "\n\r\x1b") {
		t.Fatalf("error text carried control characters: %q", err.Error())
	}
}

func TestBackoffIsBoundedAndHonoursRetryAfter(t *testing.T) {
	for attempt := 1; attempt <= 6; attempt++ {
		if d := backoff(attempt, 0); d < 0 || d > 3*time.Second {
			t.Errorf("backoff(%d) = %v, outside the expected bound", attempt, d)
		}
	}
	if d := backoff(1, 2*time.Second); d != 2*time.Second {
		t.Errorf("backoff with Retry-After = %v, want it honoured", d)
	}
	// A hostile provider must not be able to park us for hours.
	if d := backoff(1, 9*time.Hour); d > 5*time.Second {
		t.Errorf("backoff with an absurd Retry-After = %v, want it capped", d)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"3", 3 * time.Second},
		{"0", 0},
		{"-5", 0},
		{"not a number", 0},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseRetryAfter(tc.in); got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
