package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/version"
)

// NewRootCommand returns the standalone `howlinstinct` command.
func NewRootCommand() *cobra.Command {
	return newInstinctCommand("howlinstinct",
		"Fast, bounded semantic judgments with explicit uncertainty")
}

// NewInstinctCommand returns the same subtree named `instinct`, so that an
// umbrella binary can mount it without HowlInstinct having to know that the
// umbrella exists.
func NewInstinctCommand() *cobra.Command {
	return newInstinctCommand("instinct",
		"Fast, bounded semantic judgments with explicit uncertainty")
}

func newInstinctCommand(use, short string) *cobra.Command {
	root := &cobra.Command{
		Use:     use,
		Short:   short,
		Version: version.Version,
		Long: short + ".\n\n" +
			"HowlInstinct answers small bounded questions about supplied state and\n" +
			"returns typed judgments carrying explicit uncertainty and provenance.\n\n" +
			"It does not decide what should happen as a result. Confidence is not\n" +
			"permission: escalation is reported only against a threshold the caller\n" +
			"supplies, and acting on a judgment is the caller's responsibility.",

		// main owns all error reporting and the process exit code, so cobra
		// must not print or decide either.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(
		newDecideCommand(),
		newEvalCommand(),
		newProvidersCommand(),
		newDoctorCommand(),
		newVersionCommand(),
	)
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.Version)
			return nil
		},
	}
}
