package jev

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// DefaultPath is the System One endpoint path.
const DefaultPath = "/v1/systemone"

// Config describes how to reach a Jev-compatible endpoint.
//
// No field here carries a secret. The credential is named indirectly, by the
// environment variable that holds it, so that a config file, a flag, a log
// line, a receipt, and a bug report can all quote the configuration in full
// without ever quoting the key.
type Config struct {
	// BaseURL is the endpoint origin, for example https://api.example.com or
	// http://127.0.0.1:8080.
	BaseURL string

	// Path overrides the endpoint path. Empty means DefaultPath.
	Path string

	// Model names the model to request. It is passed through verbatim and
	// never defaulted to a vendor's model name.
	Model string

	// APIKeyEnv names the environment variable holding the bearer token.
	// Empty means the endpoint needs no credential, which is normal for a
	// local server.
	APIKeyEnv string

	// Timeout bounds a single HTTP attempt. The overall call is additionally
	// bounded by the caller's context.
	Timeout time.Duration

	// MaxRetries bounds retry attempts after the first. Retries are only
	// ever made for failures that are plausibly transient.
	MaxRetries int

	// MaxResponseBytes caps the response body read. A provider is untrusted
	// network input and must not be able to exhaust memory.
	MaxResponseBytes int64

	// StrictSchema rejects a response carrying fields this adapter does not
	// know. It defaults to true.
	//
	// The trade-off is deliberate. Strict means a provider that adds a
	// harmless field breaks this adapter until it is updated; permissive
	// means a provider that renames or restructures a field we rely on
	// degrades silently into judgments built from zero values. For a
	// component whose entire premise is that provider output is untrusted,
	// failing loudly on drift is the consistent choice, and operators who
	// need the other trade-off can set this false.
	StrictSchema bool

	// HTTPClient overrides the client, primarily for tests.
	HTTPClient *http.Client
}

// DefaultConfig returns the conservative defaults, with no endpoint set.
func DefaultConfig() Config {
	return Config{
		Path:             DefaultPath,
		Timeout:          20 * time.Second,
		MaxRetries:       2,
		MaxResponseBytes: instinct.DefaultLimits().MaxResponseBytes,
		StrictSchema:     true,
	}
}

// endpoint returns the fully resolved request URL.
func (c Config) endpoint() (string, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return "", instinct.Errorf(instinct.KindConfiguration, "jev",
			"no base_url configured for the jev provider")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", instinct.Wrap(instinct.KindConfiguration, "jev", err,
			"base_url %q is not a valid URL", c.BaseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", instinct.Errorf(instinct.KindConfiguration, "jev",
			"base_url %q must use http or https, got scheme %q", c.BaseURL, u.Scheme)
	}
	if u.Host == "" {
		return "", instinct.Errorf(instinct.KindConfiguration, "jev",
			"base_url %q has no host", c.BaseURL)
	}
	path := c.Path
	if path == "" {
		path = DefaultPath
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	// The caller's credentials must never travel in the URL, and must never
	// be echoed anywhere downstream. Strip them at the boundary.
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// apiKey reads the credential from the environment.
//
// The key is read at call time rather than cached at construction so that it
// is never held longer than needed, and it is returned to a single caller
// that puts it straight into a header.
func (c Config) apiKey() (string, error) {
	if c.APIKeyEnv == "" {
		return "", nil
	}
	key := os.Getenv(c.APIKeyEnv)
	if key == "" {
		return "", instinct.Errorf(instinct.KindConfiguration, "jev",
			"environment variable %s is empty or unset, but the jev provider is configured to read its credential from it",
			c.APIKeyEnv)
	}
	return key, nil
}

// Describe renders the configuration for humans. It names the credential's
// environment variable and never its value.
func (c Config) Describe() string {
	ep, err := c.endpoint()
	if err != nil {
		ep = "(unresolved)"
	}
	cred := "none"
	if c.APIKeyEnv != "" {
		cred = "$" + c.APIKeyEnv
		if os.Getenv(c.APIKeyEnv) == "" {
			cred += " (unset)"
		} else {
			cred += " (set)"
		}
	}
	model := c.Model
	if model == "" {
		model = "(unset)"
	}
	return fmt.Sprintf("endpoint=%s model=%s credential=%s timeout=%s retries=%d strict_schema=%t",
		ep, model, cred, c.Timeout, c.MaxRetries, c.StrictSchema)
}
