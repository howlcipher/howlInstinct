package cli

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/howlcipher/howlinstinct/internal/eval"
	"github.com/howlcipher/howlinstinct/internal/version"
	"github.com/howlcipher/howlinstinct/pkg/receipt"
)

// InteropSchema identifies the descriptor printed by `interop --json`, from
// the Howl interop contract
// (https://github.com/howlcipher/howl/blob/main/docs/INTEROP.md).
const InteropSchema = "howl.interop/v1"

// ResultSchema identifies the result envelope `providers --json` and
// `doctor --json` print.
const ResultSchema = "howl.result/v1"

// emits names the JSON documents each command can print or write.
var emits = map[string][]string{
	"decide":    {OutputSchema, receipt.Schema},
	"eval":      {eval.ReportSchema},
	"providers": {ResultSchema},
	"doctor":    {ResultSchema},
	"interop":   {InteropSchema},
}

var accepts = map[string][]string{
	"eval": {eval.DatasetSchema},
}

type interopCommand struct {
	Path    []string `json:"path"`
	Flags   []string `json:"flags"`
	JSON    bool     `json:"json"`
	Emits   []string `json:"emits,omitempty"`
	Accepts []string `json:"accepts,omitempty"`
}

type interopDescriptor struct {
	Schema    string            `json:"schema"`
	Component string            `json:"component"`
	Version   string            `json:"version"`
	Commands  []interopCommand  `json:"commands"`
	ExitCodes map[string]string `json:"exit_codes"`
	Env       []string          `json:"env"`
	Consumes  []any             `json:"consumes"`
}

// describe builds the descriptor from the live command tree, so it cannot
// list a command or flag the CLI does not accept.
func describe(root *cobra.Command) interopDescriptor {
	d := interopDescriptor{
		Schema:    InteropSchema,
		Component: "howlinstinct",
		Version:   strings.TrimPrefix(version.Version, "v"),
		ExitCodes: map[string]string{
			strconv.Itoa(ExitOK):                      "ok; the answer is in the output, never in the exit status",
			"1":                                       "unclassified error",
			strconv.Itoa(ExitBadInput):                "malformed or out-of-bounds request, or bad usage",
			strconv.Itoa(ExitConfig):                  "configuration or credential problem",
			strconv.Itoa(ExitProviderUnavailable):     "provider unreachable or refused to serve",
			strconv.Itoa(ExitProviderResponseInvalid): "provider answered but the answer failed validation",
			strconv.Itoa(ExitTimeout):                 "deadline elapsed",
			strconv.Itoa(ExitEvalGate):                "evaluation ran and missed a caller-supplied threshold",
		},
		Env: []string{
			"HOWLINSTINCT_PROVIDER", "HOWLINSTINCT_BASE_URL", "HOWLINSTINCT_MODEL", "HOWLINSTINCT_API_KEY_ENV",
			"HOWLINSTINCT_TIMEOUT", "HOWLINSTINCT_MAX_RETRIES", "HOWLINSTINCT_LOCAL_CONFIG",
		},
		Consumes: []any{},
	}
	// cobra registers --version only on the command it executes; register it
	// here so the root's own flags are complete whichever command runs.
	root.InitDefaultVersionFlag()
	d.Commands = append(d.Commands, interopCommand{Path: []string{}, Flags: flagNames(root.Flags()), JSON: false})
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			p := append(append([]string{}, path...), sub.Name())
			flags := flagNames(sub.Flags())
			jsonOut := false
			for _, f := range flags {
				jsonOut = jsonOut || f == "--json"
			}
			d.Commands = append(d.Commands, interopCommand{
				Path: p, Flags: flags, JSON: jsonOut,
				Emits: emits[sub.Name()], Accepts: accepts[sub.Name()],
			})
			walk(sub, p)
		}
	}
	walk(root, nil)
	return d
}

// flagNames lists a flag set's long names, plus single-letter shorthands.
func flagNames(fs *pflag.FlagSet) []string {
	names := []string{}
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		names = append(names, "--"+f.Name)
		if f.Shorthand != "" {
			names = append(names, "-"+f.Shorthand)
		}
	})
	sort.Strings(names)
	return names
}

func newInteropCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "interop",
		Short: "Print the Howl interop descriptor other components check",
		Long: "Print the howl.interop/v1 descriptor: every command and flag, the JSON\n" +
			"documents each command emits, and the exit codes. Other Howl components\n" +
			"and `howl doctor` read it to check the commands they run still exist.\n" +
			"The output is JSON with or without --json.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(describe(cmd.Root()))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON (the default)")
	return cmd
}

// resultEnvelope is the howl.result/v1 envelope.
type resultEnvelope struct {
	Schema    string   `json:"schema"`
	Component string   `json:"component"`
	Command   []string `json:"command"`
	Status    string   `json:"status"`
	Data      any      `json:"data"`
	Error     any      `json:"error"`
}

func writeResult(cmd *cobra.Command, status string, data any, errValue any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(resultEnvelope{
		Schema: ResultSchema, Component: "howlinstinct", Command: []string{cmd.Name()},
		Status: status, Data: data, Error: errValue,
	})
}
