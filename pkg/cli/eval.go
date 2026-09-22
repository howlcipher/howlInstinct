package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/config"
	"github.com/howlcipher/howlinstinct/internal/decision"
	"github.com/howlcipher/howlinstinct/internal/eval"
	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

type evalFlags struct {
	provider providerFlags

	dataset string
	jsonOut bool

	minAccuracy string

	minConfidence string
	minMargin     string
}

func newEvalCommand() *cobra.Command {
	f := &evalFlags{}

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Measure a provider against a version-controlled dataset",
		Long: "Measure a provider against a version-controlled dataset.\n\n" +
			"Metrics are reported only when the data supports them. A metric that\n" +
			"cannot be computed is reported as unavailable, with the reason, rather\n" +
			"than as zero.\n\n" +
			"Results describe one provider against one dataset at one moment. They\n" +
			"are not a claim that a decision class is fit for any particular use.",
		Args: cobra.NoArgs,
		Example: "  howlinstinct eval --dataset evals/routing/cases.yaml\n" +
			"  howlinstinct eval --dataset evals/change_risk/cases.yaml --json\n" +
			"  howlinstinct eval --dataset evals/routing/cases.yaml --min-accuracy 0.6",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEval(cmd, f)
		},
	}

	f.provider.register(cmd)
	fs := cmd.Flags()
	fs.StringVar(&f.dataset, "dataset", "", "path to a YAML or JSON evaluation dataset (required)")
	fs.BoolVar(&f.jsonOut, "json", false, "emit machine-readable JSON")
	fs.StringVar(&f.minAccuracy, "min-accuracy", "",
		"exit non-zero when accuracy falls below this, for use as a CI gate")
	fs.StringVar(&f.minConfidence, "min-provider-confidence", "",
		"apply this caller threshold to every case, to measure escalation rate")
	fs.StringVar(&f.minMargin, "min-instinct-margin", "",
		"apply this caller threshold to every case, to measure escalation rate")

	return cmd
}

func runEval(cmd *cobra.Command, f *evalFlags) error {
	if f.dataset == "" {
		return exitf(ExitBadInput, "--dataset is required")
	}

	cfg, err := config.Load(f.provider.overrides(cmd))
	if err != nil {
		return asExit(err)
	}
	ds, err := eval.Load(f.dataset)
	if err != nil {
		return asExit(err)
	}
	provider, err := buildProvider(cfg)
	if err != nil {
		return asExit(err)
	}

	rule, err := escalationRule(f.minConfidence, f.minMargin)
	if err != nil {
		return asExit(err)
	}

	engine := decision.New(provider,
		decision.WithLimits(cfg.Limits),
		decision.WithTimeout(cfg.Timeout))

	report, err := eval.Run(cmd.Context(), ds, engine, eval.Options{
		Escalation:   rule,
		ProviderName: provider.Name(),
		Model:        cfg.Model,
		Endpoint:     receipt.SafeEndpoint(providerEndpoint(provider)),
	})
	if err != nil {
		return asExit(err)
	}

	w := cmd.OutOrStdout()
	if f.jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return asExit(fmt.Errorf("writing JSON report: %w", err))
		}
	} else {
		report.WriteHuman(w)
	}

	return applyGate(f.minAccuracy, report)
}

// applyGate enforces a threshold the caller asked for.
//
// The gate lives here, in the command, rather than in the harness: the
// harness measures, and whether a number is good enough is a policy question
// that belongs to whoever is running it.
func applyGate(minAccuracy string, report eval.Report) error {
	if minAccuracy == "" {
		return nil
	}
	var threshold float64
	if _, err := fmt.Sscanf(minAccuracy, "%g", &threshold); err != nil {
		return exitf(ExitBadInput, "--min-accuracy must be a number, got %q", minAccuracy)
	}
	if report.Metrics.Accuracy == nil {
		return exitf(ExitEvalGate,
			"accuracy could not be measured (%s), so the --min-accuracy gate cannot pass",
			orUnknown(report.Metrics.AccuracyUnavailable))
	}
	if *report.Metrics.Accuracy < threshold {
		return exitf(ExitEvalGate, "accuracy %.4f is below the required %.4f",
			*report.Metrics.Accuracy, threshold)
	}
	return nil
}

func orUnknown(u eval.Unavailable) string {
	if u == "" {
		return "reason unknown"
	}
	return string(u)
}
