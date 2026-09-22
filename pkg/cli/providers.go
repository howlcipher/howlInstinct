package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/config"
	"github.com/howlcipher/howlinstinct/internal/provider/mock"
)

func newProvidersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List the available provider adapters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "mock  deterministic offline baseline. No network, credentials, or hardware.")
			fmt.Fprintf(w, "      Model %s. A lexical overlap heuristic, not a semantic model:\n", mock.Model)
			fmt.Fprintln(w, "      it exercises the plumbing and gives evaluations a baseline to beat.")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "jev   any endpoint speaking the Jev-compatible System One contract")
			fmt.Fprintln(w, "      (POST /v1/systemone). Configure --base-url, --model, and")
			fmt.Fprintln(w, "      --api-key-env. No vendor, host, or model is built in.")
			fmt.Fprintln(w)
			fmt.Fprintf(w, "The default provider is %q so that a fresh install works offline.\n",
				config.ProviderMock)
			return nil
		},
	}
}
