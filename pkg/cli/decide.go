package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/config"
	"github.com/howlcipher/howlinstinct/internal/decision"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

// OutputSchema identifies the machine-readable shape of `decide --json`.
// It is versioned because HowlPlane and others are expected to parse it.
const OutputSchema = "howlinstinct.decide_output/v1"

// DecideOutput is the stable machine-readable result of a decision.
//
// Human and JSON rendering read from this same value, which is what
// guarantees that formatting can never change a semantic field: the pretty
// printer has no other source of truth to drift from.
type DecideOutput struct {
	Schema    string `json:"schema"`
	BatchID   string `json:"batch_id"`
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	LatencyMS int64  `json:"latency_ms"`

	Judgments map[string]instinct.Judgment `json:"judgments"`
	Receipts  []receipt.DecisionReceipt    `json:"receipts"`
}

type decideFlags struct {
	provider providerFlags

	input       string
	jsonOut     bool
	retainState bool

	// Single-question construction.
	id           string
	typ          string
	state        string
	question     string
	options      []string
	levels       []string
	trueMeaning  string
	falseMeaning string

	// Caller-supplied escalation thresholds. Absent means no rule at all,
	// which is why these are strings rather than floats: there must be no way
	// to accidentally supply a default threshold.
	minConfidence string
	minMargin     string
}

func newDecideCommand() *cobra.Command {
	f := &decideFlags{}

	cmd := &cobra.Command{
		Use:   "decide",
		Short: "Ask one or more bounded questions about some state",
		Long: "Ask one or more bounded questions about some state.\n\n" +
			"Input is either built from flags for a single question, or read as JSON\n" +
			"from a file or stdin for a batch. Question identifiers are preserved\n" +
			"exactly and echoed in every judgment and receipt.\n\n" +
			"The exit status reports whether the tool worked, never what the answer\n" +
			"was: a noul answering \"no\" exits 0.",
		Args: cobra.NoArgs,
		Example: "  howlinstinct decide --type noul \\\n" +
			"    --state \"All payment requests are returning HTTP 500.\" \\\n" +
			"    --question \"Is this likely an active production incident?\"\n\n" +
			"  howlinstinct decide --input request.json --json\n\n" +
			"  cat request.json | howlinstinct decide --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDecide(cmd, f)
		},
	}

	f.provider.register(cmd)
	fs := cmd.Flags()
	fs.StringVar(&f.input, "input", "", "read a JSON request from this file, or - for stdin")
	fs.BoolVar(&f.jsonOut, "json", false, "emit machine-readable JSON")
	fs.BoolVar(&f.retainState, "retain-state", false,
		"store the raw state in the receipt (off by default: state often contains sensitive material)")

	fs.StringVar(&f.id, "id", "", "question identifier (default: the decision type)")
	fs.StringVar(&f.typ, "type", "", "decision type: noul, choice, score, or classify")
	fs.StringVar(&f.state, "state", "", "the state to judge")
	fs.StringVar(&f.question, "question", "", "the question to ask about the state")
	fs.StringSliceVar(&f.options, "options", nil, "options for a choice or classify question")
	fs.StringSliceVar(&f.levels, "levels", nil, "ordered levels for a score question, lowest first")
	fs.StringVar(&f.trueMeaning, "true-meaning", "", "what yes means, for a noul question")
	fs.StringVar(&f.falseMeaning, "false-meaning", "", "what no means, for a noul question")

	fs.StringVar(&f.minConfidence, "min-provider-confidence", "",
		"escalate when the provider's reported confidence is below this (no default: omitting it means no rule)")
	fs.StringVar(&f.minMargin, "min-instinct-margin", "",
		"escalate when the derived instinct margin is below this (no default: omitting it means no rule)")

	return cmd
}

func runDecide(cmd *cobra.Command, f *decideFlags) error {
	cfg, err := config.Load(f.provider.overrides(cmd))
	if err != nil {
		return asExit(err)
	}

	req, err := buildRequest(cmd, f, cfg.Limits)
	if err != nil {
		return asExit(err)
	}

	provider, err := buildProvider(cfg)
	if err != nil {
		return asExit(err)
	}

	engine := decision.New(provider,
		decision.WithLimits(cfg.Limits),
		decision.WithTimeout(cfg.Timeout))

	res, decideErr := engine.Decide(cmd.Context(), req)

	// A failed decision still produced receipts recording that it was
	// attempted, so they are rendered before the error is returned.
	out := DecideOutput{
		Schema:    OutputSchema,
		BatchID:   res.BatchID,
		Provider:  res.Response.Provider,
		Model:     res.Response.Model,
		Endpoint:  receipt.SafeEndpoint(providerEndpoint(provider)),
		LatencyMS: res.Response.LatencyMS,
		Judgments: res.Response.Judgments,
		Receipts:  res.Receipts,
	}
	if len(out.Judgments) > 0 || len(out.Receipts) > 0 {
		if err := render(cmd, f.jsonOut, out); err != nil {
			return asExit(err)
		}
	}
	return asExit(decideErr)
}

// buildRequest assembles a request from a JSON document or from flags.
func buildRequest(cmd *cobra.Command, f *decideFlags, lim instinct.Limits) (instinct.Request, error) {
	source, err := requestReader(cmd, f)
	if err != nil {
		return instinct.Request{}, err
	}
	if source != nil {
		req, err := instinct.DecodeRequest(source, lim)
		if err != nil {
			return req, err
		}
		if f.retainState {
			req.RetainState = true
		}
		return req, nil
	}
	return buildSingleQuestion(f, lim)
}

// requestReader returns a reader for a JSON request, or nil when the request
// is to be assembled from flags.
func requestReader(cmd *cobra.Command, f *decideFlags) (io.Reader, error) {
	if f.input == "-" {
		return cmd.InOrStdin(), nil
	}
	if f.input != "" {
		// #nosec G304 -- the path is supplied by the operator running the
		// command, not by untrusted input.
		file, err := os.Open(f.input)
		if err != nil {
			return nil, instinct.Wrap(instinct.KindInvalidInput, "decide", err,
				"opening request file")
		}
		cobra.OnFinalize(func() { _ = file.Close() })
		return file, nil
	}
	if f.state != "" || f.typ != "" {
		return nil, nil
	}
	// Nothing on the command line: accept a piped document, but never block
	// on an interactive terminal waiting for input the user did not offer.
	if in := cmd.InOrStdin(); in != os.Stdin || isPiped(os.Stdin) {
		return in, nil
	}
	return nil, instinct.Errorf(instinct.KindInvalidInput, "decide",
		"no request supplied: pass --state and --type, or --input FILE, or pipe JSON on stdin")
}

func isPiped(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func buildSingleQuestion(f *decideFlags, lim instinct.Limits) (instinct.Request, error) {
	var req instinct.Request

	typ := instinct.DecisionType(strings.TrimSpace(f.typ))
	if !typ.IsValid() {
		return req, instinct.Errorf(instinct.KindInvalidInput, "decide",
			"--type must be one of noul, choice, score, classify; got %q", f.typ)
	}
	if f.question == "" {
		return req, instinct.Errorf(instinct.KindInvalidInput, "decide",
			"--question is required when building a question from flags")
	}

	q := instinct.Question{
		Type:         typ,
		Instructions: f.question,
		TrueMeaning:  f.trueMeaning,
		FalseMeaning: f.falseMeaning,
	}
	for _, name := range f.options {
		q.Options = append(q.Options, instinct.Option{Name: strings.TrimSpace(name)})
	}
	for _, name := range f.levels {
		q.Levels = append(q.Levels, instinct.Level{Name: strings.TrimSpace(name)})
	}

	id := f.id
	if id == "" {
		id = string(typ)
	}

	req = instinct.Request{
		State:       f.state,
		Questions:   map[string]instinct.Question{id: q},
		RetainState: f.retainState,
	}

	rule, err := escalationRule(f)
	if err != nil {
		return req, err
	}
	if !rule.IsZero() {
		req.Escalation = map[string]instinct.EscalationRule{id: rule}
	}
	return req, req.Validate(lim)
}

// escalationRule reads the caller's thresholds. A flag that was not supplied
// contributes nothing: there is no default threshold anywhere in this tool.
func escalationRule(f *decideFlags) (instinct.EscalationRule, error) {
	var rule instinct.EscalationRule
	for _, m := range []struct {
		flag string
		raw  string
		dst  **float64
	}{
		{"--min-provider-confidence", f.minConfidence, &rule.MinProviderConfidence},
		{"--min-instinct-margin", f.minMargin, &rule.MinInstinctMargin},
	} {
		if m.raw == "" {
			continue
		}
		var v float64
		if _, err := fmt.Sscanf(m.raw, "%g", &v); err != nil {
			return rule, instinct.Errorf(instinct.KindInvalidInput, "decide",
				"%s must be a number between 0 and 1, got %q", m.flag, m.raw)
		}
		*m.dst = instinct.Float(v)
	}
	return rule, nil
}

// render writes the result as JSON or as human-readable text.
func render(cmd *cobra.Command, asJSON bool, out DecideOutput) error {
	w := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("writing JSON output: %w", err)
		}
		return nil
	}
	renderHuman(w, out)
	return nil
}

func renderHuman(w io.Writer, out DecideOutput) {
	ids := make([]string, 0, len(out.Judgments))
	for id := range out.Judgments {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		j := out.Judgments[id]
		fmt.Fprintf(w, "%s  [%s]\n", id, j.Type)
		fmt.Fprintf(w, "  answer:     %s\n", answerText(j))

		// Probability and confidence are printed under separate labels, and
		// a value the provider did not report is shown as unavailable rather
		// than as a number. Printing "0.00" for an absent confidence would
		// be the display layer inventing data.
		if j.ProbabilityYes != nil {
			fmt.Fprintf(w, "  P(yes):     %.4f\n", *j.ProbabilityYes)
		}
		if len(j.Probabilities) > 0 {
			fmt.Fprintf(w, "  spread:     %s\n", distributionText(j))
		}
		fmt.Fprintf(w, "  provider confidence: %s\n", floatText(j.ProviderConfidence,
			"not reported by this provider"))
		fmt.Fprintf(w, "  instinct margin:     %s\n", floatText(j.InstinctMargin,
			"unavailable"))
		fmt.Fprintf(w, "  outcome:    %s\n", j.Outcome)
		if j.NeedsEscalation {
			fmt.Fprintf(w, "  escalate:   yes (%s)\n", j.EscalationReason)
		}
		if j.Error != "" {
			fmt.Fprintf(w, "  error:      %s\n", j.Error)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "provider %s", out.Provider)
	if out.Model != "" {
		fmt.Fprintf(w, " model %s", out.Model)
	}
	fmt.Fprintf(w, " in %dms, batch %s\n", out.LatencyMS, out.BatchID)
	fmt.Fprintln(w,
		"\nHowlInstinct reports judgments, not permission. Acting on these is the caller's decision.")
}

func answerText(j instinct.Judgment) string {
	switch {
	case j.Bool != nil:
		if *j.Bool {
			return "yes"
		}
		return "no"
	case j.Choice != nil:
		return *j.Choice
	case j.Score != nil:
		return fmt.Sprintf("%.3f%s", *j.Score, legendText(j))
	default:
		return "(none)"
	}
}

func legendText(j instinct.Judgment) string {
	if len(j.Legend) == 0 {
		return ""
	}
	return " on [" + strings.Join(j.Legend, " < ") + "]"
}

func distributionText(j instinct.Judgment) string {
	keys := make([]string, 0, len(j.Probabilities))
	for k := range j.Probabilities {
		keys = append(keys, k)
	}
	// Legend order for a score, alphabetical otherwise, so the output is
	// deterministic either way.
	if len(j.Legend) == len(keys) {
		keys = append([]string(nil), j.Legend...)
	} else {
		sort.Strings(keys)
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.3f", k, j.Probabilities[k]))
	}
	return strings.Join(parts, "  ")
}

func floatText(v *float64, absent string) string {
	if v == nil {
		return absent
	}
	return fmt.Sprintf("%.4f", *v)
}
