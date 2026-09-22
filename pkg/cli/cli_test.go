package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/config"
)

// isolate removes any influence from the developer's own environment, so a
// test measures the code rather than the machine it runs on.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.LocalConfigEnv, "")
	for _, e := range []string{
		config.EnvProvider, config.EnvBaseURL, config.EnvModel,
		config.EnvAPIKeyEnv, config.EnvTimeout, config.EnvMaxRetries,
	} {
		t.Setenv(e, "")
	}
}

type result struct {
	stdout string
	stderr string
	code   int
	err    error
}

// run executes the command tree and reports what a shell would have seen.
func run(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	isolate(t)

	var out, errBuf bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(context.Background())

	res := result{stdout: out.String(), stderr: errBuf.String(), err: err}
	if err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			res.code = exitErr.Code
		} else {
			res.code = 1
		}
	}
	return res
}

const batchJSON = `{
  "state": "All payment requests are returning HTTP 500 across every region.",
  "questions": {
    "is_incident": {"type": "noul", "instructions": "Is this an active incident?"},
    "category": {"type": "classify", "instructions": "Category?",
                 "options": [{"name": "billing"}, {"name": "outage"}]},
    "severity": {"type": "score", "instructions": "How severe?",
                 "levels": [{"name": "low"}, {"name": "high"}]}
  }
}`

// The exit codes are a contract with scripts. Pinning them here means a
// change to the table is a deliberate act rather than an accident.
func TestExitCodeTableIsStable(t *testing.T) {
	want := map[string]int{
		"ok":                   0,
		"bad input":            2,
		"config":               3,
		"provider unavailable": 4,
		"provider invalid":     5,
		"timeout":              6,
		"eval gate":            7,
	}
	got := map[string]int{
		"ok":                   ExitOK,
		"bad input":            ExitBadInput,
		"config":               ExitConfig,
		"provider unavailable": ExitProviderUnavailable,
		"provider invalid":     ExitProviderResponseInvalid,
		"timeout":              ExitTimeout,
		"eval gate":            ExitEvalGate,
	}
	for name, code := range want {
		if got[name] != code {
			t.Errorf("exit code for %s = %d, want %d", name, got[name], code)
		}
	}
}

// The single most important CLI behaviour: the exit status reports whether
// the tool worked, never what the answer was. Encoding the answer would make
// a confident "no" indistinguishable from a crashed provider.
func TestExitStatusNeverEncodesTheAnswer(t *testing.T) {
	// A state sharing no vocabulary with the question drives the lexical
	// mock toward "no"; either answer must still exit 0.
	for _, state := range []string{
		"everything is completely fine and nothing is wrong",
		"total catastrophic outage every region down",
	} {
		res := run(t, "", "decide", "--type", "noul",
			"--state", state, "--question", "Is this an outage requiring investigation?")
		if res.code != ExitOK {
			t.Fatalf("decide on %q exited %d (%v), want 0 whatever the answer", state, res.code, res.err)
		}
		if !strings.Contains(res.stdout, "answer:") {
			t.Fatalf("no answer was printed for %q:\n%s", state, res.stdout)
		}
	}
}

func TestDecideFromFlagsProducesJSON(t *testing.T) {
	res := run(t, "", "decide", "--json", "--type", "noul",
		"--state", "All payment requests are returning HTTP 500.",
		"--question", "Is this an active production incident?")
	if res.code != ExitOK {
		t.Fatalf("exit %d: %v\n%s", res.code, res.err, res.stderr)
	}

	var out DecideOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, res.stdout)
	}
	if out.Schema != OutputSchema {
		t.Errorf("schema = %q, want %q", out.Schema, OutputSchema)
	}
	if len(out.Judgments) != 1 || len(out.Receipts) != 1 {
		t.Fatalf("got %d judgments and %d receipts, want 1 of each",
			len(out.Judgments), len(out.Receipts))
	}
	for _, r := range out.Receipts {
		if err := r.Validate(); err != nil {
			t.Errorf("emitted receipt is invalid: %v", err)
		}
	}
}

func TestDecideReadsABatchFromStdin(t *testing.T) {
	res := run(t, batchJSON, "decide", "--json", "--input", "-")
	if res.code != ExitOK {
		t.Fatalf("exit %d: %v\n%s", res.code, res.err, res.stderr)
	}

	var out DecideOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if len(out.Judgments) != 3 {
		t.Fatalf("got %d judgments, want 3", len(out.Judgments))
	}
	// Question identifiers are the caller's, and must come back untouched.
	for _, id := range []string{"is_incident", "category", "severity"} {
		if _, ok := out.Judgments[id]; !ok {
			t.Errorf("judgment %q is missing; identifiers must be preserved exactly", id)
		}
	}
	// Classify must be recorded as classify, with the lowering noted.
	if j := out.Judgments["category"]; j.CompiledTo != "choice" {
		t.Errorf("compiled_to = %q, want choice", j.CompiledTo)
	}
}

func TestDecideReadsABatchFromAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "req.json")
	if err := os.WriteFile(path, []byte(batchJSON), 0o600); err != nil {
		t.Fatalf("writing request: %v", err)
	}
	res := run(t, "", "decide", "--json", "--input", path)
	if res.code != ExitOK {
		t.Fatalf("exit %d: %v\n%s", res.code, res.err, res.stderr)
	}
	if !strings.Contains(res.stdout, "is_incident") {
		t.Fatalf("file input did not produce the expected judgments:\n%s", res.stdout)
	}
}

// Human formatting must never change a semantic field. Both renderings read
// the same value, and this asserts they agree about the answer.
func TestHumanAndJSONAgreeOnTheAnswer(t *testing.T) {
	human := run(t, batchJSON, "decide", "--input", "-")
	machine := run(t, batchJSON, "decide", "--json", "--input", "-")

	var out DecideOutput
	if err := json.Unmarshal([]byte(machine.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	j := out.Judgments["category"]
	if j.Choice == nil {
		t.Fatal("no choice in the machine-readable output")
	}
	if !strings.Contains(human.stdout, *j.Choice) {
		t.Fatalf("human output does not report the same choice %q:\n%s", *j.Choice, human.stdout)
	}
	// A noul has no provider confidence, and the human rendering must say so
	// rather than printing a number nobody reported.
	if !strings.Contains(human.stdout, "not reported by this provider") {
		t.Fatalf("human output did not mark the absent confidence:\n%s", human.stdout)
	}
	if strings.Contains(human.stdout, "provider confidence: 0.0000") {
		t.Fatalf("human output printed a fabricated zero confidence:\n%s", human.stdout)
	}
}

// Machine-readable output must be the only thing on stdout, or piping it
// into a JSON parser breaks.
func TestJSONOutputIsAloneOnStdout(t *testing.T) {
	res := run(t, batchJSON, "decide", "--json", "--input", "-")
	var any map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &any); err != nil {
		t.Fatalf("stdout was not parseable as a single JSON document: %v\n%s", err, res.stdout)
	}
}

func TestErrorsMapToTheirExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"unknown decision type", []string{"decide", "--type", "oracle", "--state", "s", "--question", "q"}, ExitBadInput},
		{"missing question", []string{"decide", "--type", "noul", "--state", "s"}, ExitBadInput},
		{"empty state", []string{"decide", "--type", "noul", "--state", "", "--question", "q"}, ExitBadInput},
		{"choice with no options", []string{"decide", "--type", "choice", "--state", "s", "--question", "q"}, ExitBadInput},
		{"malformed json", []string{"decide", "--input", "-"}, ExitBadInput},
		{"unknown provider", []string{"decide", "--provider", "oracle", "--type", "noul", "--state", "s", "--question", "q"}, ExitConfig},
		{"jev with no endpoint", []string{"decide", "--provider", "jev", "--type", "noul", "--state", "s", "--question", "q"}, ExitConfig},
		{"eval with no dataset", []string{"eval"}, ExitBadInput},
		{"eval with a missing dataset", []string{"eval", "--dataset", "/nonexistent/cases.yaml"}, ExitBadInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdin := ""
			if tc.name == "malformed json" {
				stdin = "{not json"
			}
			res := run(t, stdin, tc.args...)
			if res.code != tc.want {
				t.Fatalf("exit %d (%v), want %d", res.code, res.err, tc.want)
			}
		})
	}
}

// An unreachable provider and a provider returning nonsense are different
// problems and must be distinguishable by exit code alone.
func TestProviderFailuresAreDistinguishedByExitCode(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		res := run(t, "", "decide", "--provider", "jev", "--base-url", srv.URL,
			"--max-retries", "0", "--type", "noul", "--state", "s", "--question", "q")
		if res.code != ExitProviderUnavailable {
			t.Fatalf("exit %d (%v), want %d", res.code, res.err, ExitProviderUnavailable)
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// A well-formed envelope answering a question nobody asked.
			_, _ = io.WriteString(w, `{"answers":{"ghost":{"noul":0.5}}}`)
		}))
		defer srv.Close()

		res := run(t, "", "decide", "--provider", "jev", "--base-url", srv.URL,
			"--max-retries", "0", "--type", "noul", "--state", "s", "--question", "q")
		if res.code != ExitProviderResponseInvalid {
			t.Fatalf("exit %d (%v), want %d", res.code, res.err, ExitProviderResponseInvalid)
		}
	})

	t.Run("bad credential is a configuration problem", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		res := run(t, "", "decide", "--provider", "jev", "--base-url", srv.URL,
			"--max-retries", "0", "--type", "noul", "--state", "s", "--question", "q")
		if res.code != ExitConfig {
			t.Fatalf("exit %d (%v), want %d", res.code, res.err, ExitConfig)
		}
	})
}

func TestTimeoutHasItsOwnExitCode(t *testing.T) {
	// Bounded rather than blocking forever: httptest's Close waits for
	// outstanding handlers, and a handler that only unblocks on client
	// disconnect makes the test hang when that signal is delayed.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	res := run(t, "", "decide", "--provider", "jev", "--base-url", srv.URL,
		"--timeout", "50ms", "--max-retries", "0",
		"--type", "noul", "--state", "s", "--question", "q")
	if res.code != ExitTimeout {
		t.Fatalf("exit %d (%v), want %d", res.code, res.err, ExitTimeout)
	}
}

// A failed decision still emits receipts, because an audit trail that omits
// the attempt is misleading about what happened.
func TestAFailedDecisionStillEmitsReceipts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := run(t, "", "decide", "--json", "--provider", "jev", "--base-url", srv.URL,
		"--max-retries", "0", "--type", "noul", "--state", "s", "--question", "q")

	var out DecideOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("no parseable output for a failed decision: %v\n%s", err, res.stdout)
	}
	if len(out.Receipts) != 1 {
		t.Fatalf("got %d receipts for a failed decision, want 1", len(out.Receipts))
	}
	if out.Receipts[0].Outcome != "UNAVAILABLE" {
		t.Fatalf("receipt outcome = %q, want UNAVAILABLE", out.Receipts[0].Outcome)
	}
}

func TestEvalGateExitsSeven(t *testing.T) {
	dataset := filepath.Join("..", "..", "evals", "routing", "cases.yaml")

	t.Run("gate not met", func(t *testing.T) {
		res := run(t, "", "eval", "--dataset", dataset, "--min-accuracy", "0.99")
		if res.code != ExitEvalGate {
			t.Fatalf("exit %d (%v), want %d", res.code, res.err, ExitEvalGate)
		}
	})

	t.Run("no gate requested", func(t *testing.T) {
		res := run(t, "", "eval", "--dataset", dataset)
		if res.code != ExitOK {
			t.Fatalf("exit %d (%v), want 0 when no gate was requested", res.code, res.err)
		}
	})
}

func TestEvalEmitsMachineReadableReport(t *testing.T) {
	res := run(t, "", "eval", "--json", "--dataset",
		filepath.Join("..", "..", "evals", "routing", "cases.yaml"))
	if res.code != ExitOK {
		t.Fatalf("exit %d: %v", res.code, res.err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
		t.Fatalf("eval output is not valid JSON: %v", err)
	}
	for _, key := range []string{"schema", "dataset", "provider", "metrics", "caveat"} {
		if _, ok := report[key]; !ok {
			t.Errorf("eval report is missing %q", key)
		}
	}
}

// A metric that could not be computed must be null with a reason, never zero.
func TestUnavailableMetricsAreNullNotZero(t *testing.T) {
	res := run(t, "", "eval", "--json", "--dataset",
		filepath.Join("..", "..", "evals", "routing", "cases.yaml"))

	var report struct {
		Metrics struct {
			MAE       *float64 `json:"mean_absolute_error"`
			MAEReason string   `json:"mean_absolute_error_unavailable_reason"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
		t.Fatalf("eval output is not valid JSON: %v", err)
	}
	if report.Metrics.MAE != nil {
		t.Fatalf("mean absolute error = %v for a classification suite, want null", *report.Metrics.MAE)
	}
	if report.Metrics.MAEReason == "" {
		t.Fatal("an unavailable metric carried no reason")
	}
}

func TestDoctorReportsConfigurationWithoutLeakingCredentials(t *testing.T) {
	const secret = "sk-live-SUPERSECRET"
	t.Setenv("HOWLINSTINCT_TEST_KEY", secret)

	res := run(t, "", "doctor", "--provider", "jev",
		"--base-url", "http://remote.example.com", "--api-key-env", "HOWLINSTINCT_TEST_KEY")
	if res.code != ExitOK {
		t.Fatalf("exit %d: %v", res.code, res.err)
	}
	if strings.Contains(res.stdout, secret) {
		t.Fatalf("doctor leaked the credential:\n%s", res.stdout)
	}
	if !strings.Contains(res.stdout, "$HOWLINSTINCT_TEST_KEY") {
		t.Errorf("doctor did not name the credential variable:\n%s", res.stdout)
	}
	// Plaintext HTTP to a remote host would put the judged state on the wire
	// in the clear, so it must be called out.
	if !strings.Contains(res.stdout, "plaintext HTTP") {
		t.Errorf("doctor did not warn about plaintext HTTP to a remote host:\n%s", res.stdout)
	}
}

func TestDoctorIsQuietForTheOfflineDefault(t *testing.T) {
	res := run(t, "", "doctor")
	if res.code != ExitOK {
		t.Fatalf("exit %d: %v", res.code, res.err)
	}
	if !strings.Contains(res.stdout, "No warnings") {
		t.Errorf("doctor warned about the safe offline default:\n%s", res.stdout)
	}
}

func TestProvidersAndVersion(t *testing.T) {
	providers := run(t, "", "providers")
	if providers.code != ExitOK {
		t.Fatalf("providers exited %d", providers.code)
	}
	for _, want := range []string{"mock", "jev"} {
		if !strings.Contains(providers.stdout, want) {
			t.Errorf("providers did not list %q:\n%s", want, providers.stdout)
		}
	}
	// The mock must not be mistaken for a model, so its description has to
	// say what it actually is.
	if !strings.Contains(providers.stdout, "not a semantic model") {
		t.Errorf("providers output oversells the mock:\n%s", providers.stdout)
	}

	version := run(t, "", "version")
	if version.code != ExitOK || strings.TrimSpace(version.stdout) == "" {
		t.Fatalf("version exited %d with output %q", version.code, version.stdout)
	}
}

// HowlInstinct judges; it does not authorize. The help text has to say so,
// because the CLI is where most people meet the tool.
func TestHelpStatesTheAuthorityBoundary(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	// The phrase wraps across a line in the rendered help, so match the
	// stable part rather than the wrapped whole.
	if !strings.Contains(out.String(), "Confidence is not") {
		t.Fatalf("root help does not state the authority boundary:\n%s", out.String())
	}
}

// The subtree must be mountable under an umbrella command, which is how the
// ecosystem's composition contract expects a component to be consumed.
func TestSubtreeIsMountable(t *testing.T) {
	umbrella := &cobra.Command{Use: "howl", SilenceErrors: true, SilenceUsage: true}
	umbrella.AddCommand(NewInstinctCommand())

	var out bytes.Buffer
	umbrella.SetOut(&out)
	umbrella.SetErr(&out)
	umbrella.SetArgs([]string{"instinct", "version"})

	if err := umbrella.Execute(); err != nil {
		t.Fatalf("mounted subtree failed: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("mounted subtree produced no output")
	}
}

// There is no default threshold anywhere in the tool. Omitting the flags must
// therefore never escalate, however uncertain the judgment is.
func TestNoEscalationWithoutACallerThreshold(t *testing.T) {
	res := run(t, "", "decide", "--json", "--type", "choice",
		"--state", "zzz qqq vvv", "--question", "Which category?",
		"--options", "billing,outage,feature")

	var out DecideOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	for id, j := range out.Judgments {
		if j.NeedsEscalation {
			t.Fatalf("judgment %q escalated with no caller threshold (reason %q)", id, j.EscalationReason)
		}
		if j.Outcome != "ACCEPTABLE_CONFIDENCE" {
			t.Fatalf("judgment %q outcome = %q, want ACCEPTABLE_CONFIDENCE", id, j.Outcome)
		}
	}
}

func TestCallerThresholdDrivesEscalation(t *testing.T) {
	res := run(t, "", "decide", "--json", "--type", "choice",
		"--state", "zzz qqq vvv", "--question", "Which category?",
		"--options", "billing,outage,feature",
		"--min-instinct-margin", "0.99")

	var out DecideOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	for id, j := range out.Judgments {
		if !j.NeedsEscalation {
			t.Fatalf("judgment %q did not escalate against a threshold it cannot meet", id)
		}
		if j.EscalationReason != "BELOW_CALLER_THRESHOLD" {
			t.Fatalf("reason = %q, want BELOW_CALLER_THRESHOLD", j.EscalationReason)
		}
	}
}

// State is not retained by default, because a receipt is durable and state
// routinely contains sensitive material.
func TestStateIsNotRetainedUnlessAsked(t *testing.T) {
	const sensitive = "card ending 4021 declined for customer 9987"

	plain := run(t, "", "decide", "--json", "--type", "noul",
		"--state", sensitive, "--question", "Is this a payment failure?")
	if strings.Contains(plain.stdout, "4021") {
		t.Fatalf("receipt retained raw state without being asked:\n%s", plain.stdout)
	}

	opted := run(t, "", "decide", "--json", "--retain-state", "--type", "noul",
		"--state", sensitive, "--question", "Is this a payment failure?")
	if !strings.Contains(opted.stdout, "4021") {
		t.Fatalf("--retain-state did not retain the state:\n%s", opted.stdout)
	}
}
