package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/howlcipher/howlinstinct/internal/config"
)

// providerFlags are the configuration flags shared by every command that
// talks to a provider. They form the highest-precedence configuration layer.
type providerFlags struct {
	provider   string
	baseURL    string
	model      string
	apiKeyEnv  string
	timeout    time.Duration
	maxRetries int
}

func (f *providerFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.provider, "provider", "",
		"provider adapter: mock or jev (default mock, which needs no network)")
	fs.StringVar(&f.baseURL, "base-url", "",
		"base URL of a Jev-compatible endpoint")
	fs.StringVar(&f.model, "model", "",
		"model identifier to request from the provider")
	fs.StringVar(&f.apiKeyEnv, "api-key-env", "",
		"name of the environment variable holding the API key (never the key itself)")
	fs.DurationVar(&f.timeout, "timeout", 0,
		"overall deadline for the call")
	fs.IntVar(&f.maxRetries, "max-retries", -1,
		"maximum retries for transient provider failures")
}

// overrides converts the flags into a configuration layer, distinguishing
// "not supplied" from "supplied as zero".
func (f *providerFlags) overrides(cmd *cobra.Command) config.Overrides {
	ov := config.Overrides{
		Provider:  f.provider,
		BaseURL:   f.baseURL,
		Model:     f.model,
		APIKeyEnv: f.apiKeyEnv,
		Timeout:   f.timeout,
	}
	// Zero retries is a meaningful choice, so the flag's presence is what
	// matters, not its value.
	if cmd.Flags().Changed("max-retries") {
		n := f.maxRetries
		if n < 0 {
			n = 0
		}
		ov.MaxRetries = &n
	}
	return ov
}
