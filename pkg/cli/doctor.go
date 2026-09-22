package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/config"
)

func newDoctorCommand() *cobra.Command {
	f := &providerFlags{}

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report the resolved configuration and flag risky settings",
		Long: "Report the resolved configuration, where each value came from, and any\n" +
			"settings worth a second look.\n\n" +
			"The output is safe to paste into a ticket: it names the environment\n" +
			"variable holding a credential and never the credential itself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()

			cfg, err := config.Load(f.overrides(cmd))
			if err != nil {
				// A configuration too broken to load is exactly what doctor
				// exists to explain, so report it legibly and still exit
				// non-zero so a script notices.
				fmt.Fprintf(w, "configuration could not be resolved:\n  %v\n", err)
				return asExit(err)
			}

			fmt.Fprintln(w, "Resolved configuration")
			fmt.Fprintln(w, "----------------------")
			fmt.Fprint(w, cfg.Describe())

			path, explicit := config.LocalConfigPath()
			fmt.Fprintf(w, "config file:   %s%s\n", path, fileState(path, explicit))

			fmt.Fprintln(w, "\nChecks")
			fmt.Fprintln(w, "------")
			warnings := runChecks(w, cfg)

			fmt.Fprintln(w)
			if warnings == 0 {
				fmt.Fprintln(w, "No warnings.")
			} else {
				fmt.Fprintf(w, "%d warning(s). None of these stop HowlInstinct running.\n", warnings)
			}
			return nil
		},
	}
	f.register(cmd)
	return cmd
}

func fileState(path string, explicit bool) string {
	if path == "" {
		return " (no home directory; only defaults, environment, and flags apply)"
	}
	if _, err := os.Stat(path); err != nil {
		if explicit {
			return " (named explicitly but MISSING)"
		}
		return " (absent, which is fine)"
	}
	return " (present)"
}

// runChecks reports settings that are legal but worth knowing about. Each is
// a warning rather than an error: an operator may have good reasons, and
// doctor's job is to inform rather than to forbid.
func runChecks(w io.Writer, cfg config.Config) int {
	var warnings int
	warn := func(format string, args ...any) {
		warnings++
		fmt.Fprintf(w, "  WARN  "+format+"\n", args...)
	}
	ok := func(format string, args ...any) {
		fmt.Fprintf(w, "  ok    "+format+"\n", args...)
	}

	if cfg.Provider == config.ProviderMock {
		ok("provider is the offline mock; no network request will be made")
		ok("no credential is needed")
		return warnings
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" {
		warn("base_url %q could not be parsed", cfg.BaseURL)
		return warnings
	}

	host := u.Hostname()
	loopback := host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"

	switch {
	case u.Scheme == "https":
		ok("endpoint uses HTTPS")
	case loopback:
		ok("endpoint is plaintext HTTP but stays on the loopback interface")
	default:
		// State is the material being judged and routinely contains logs,
		// customer records, or credentials. Sending it in the clear to a
		// remote host exposes all of it to anyone on the path.
		warn("endpoint %q uses plaintext HTTP to a remote host; state would cross the network unencrypted",
			cfg.BaseURL)
	}

	if u.User != nil {
		warn("base_url embeds credentials in the URL; put the key in an environment variable and name it with --api-key-env")
	}
	if u.RawQuery != "" {
		warn("base_url carries a query string, which is stripped before the request is sent")
	}

	switch {
	case cfg.APIKeyEnv == "":
		if !loopback {
			warn("no credential is configured for a remote endpoint; set --api-key-env if it requires one")
		} else {
			ok("no credential configured, which is normal for a local endpoint")
		}
	case os.Getenv(cfg.APIKeyEnv) == "":
		warn("$%s is unset, so authenticated calls will fail", cfg.APIKeyEnv)
	default:
		ok("credential is present in $%s", cfg.APIKeyEnv)
	}

	if cfg.Model == "" {
		warn("no model is configured; the endpoint will apply its own default")
	} else {
		ok("model is %s", cfg.Model)
	}
	return warnings
}
