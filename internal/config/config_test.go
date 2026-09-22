package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// isolate points configuration discovery at a temporary home with no config
// file, so a developer's real ~/.config cannot influence a test run.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(LocalConfigEnv, "")
	for _, e := range []string{
		EnvProvider, EnvBaseURL, EnvModel, EnvAPIKeyEnv, EnvTimeout, EnvMaxRetries,
	} {
		t.Setenv(e, "")
	}
	return home
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return path
}

// The default must work offline, with no file, no environment, and no
// credentials. A fresh checkout that cannot run is a broken checkout.
func TestDefaultsAreOfflineAndUsable(t *testing.T) {
	isolate(t)

	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider != ProviderMock {
		t.Errorf("default provider = %q, want %q", cfg.Provider, ProviderMock)
	}
	if cfg.Timeout <= 0 {
		t.Errorf("default timeout = %v, want a positive bound", cfg.Timeout)
	}
}

// Precedence is the contract: each layer is more specific to the moment than
// the one below it, so each must win over the one below it.
func TestPrecedenceFileThenEnvThenFlags(t *testing.T) {
	isolate(t)
	path := writeConfig(t, `
provider = "jev"
base_url = "https://from-file.example.com"
model = "model-from-file"
timeout = "11s"
max_retries = 1
`)
	t.Setenv(LocalConfigEnv, path)

	t.Run("file beats defaults", func(t *testing.T) {
		cfg, err := Load(Overrides{})
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.BaseURL != "https://from-file.example.com" {
			t.Errorf("base_url = %q, want the file's value", cfg.BaseURL)
		}
		if cfg.Model != "model-from-file" {
			t.Errorf("model = %q, want the file's value", cfg.Model)
		}
		if cfg.Timeout != 11*time.Second {
			t.Errorf("timeout = %v, want 11s from the file", cfg.Timeout)
		}
	})

	t.Run("environment beats file", func(t *testing.T) {
		t.Setenv(EnvBaseURL, "https://from-env.example.com")
		t.Setenv(EnvTimeout, "12s")

		cfg, err := Load(Overrides{})
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.BaseURL != "https://from-env.example.com" {
			t.Errorf("base_url = %q, want the environment's value", cfg.BaseURL)
		}
		if cfg.Timeout != 12*time.Second {
			t.Errorf("timeout = %v, want 12s from the environment", cfg.Timeout)
		}
		// A layer that says nothing must not erase the layer beneath it.
		if cfg.Model != "model-from-file" {
			t.Errorf("model = %q, want the file's value to survive", cfg.Model)
		}
	})

	t.Run("flags beat everything", func(t *testing.T) {
		t.Setenv(EnvBaseURL, "https://from-env.example.com")
		retries := 5

		cfg, err := Load(Overrides{
			BaseURL:    "https://from-flag.example.com",
			Timeout:    13 * time.Second,
			MaxRetries: &retries,
		})
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.BaseURL != "https://from-flag.example.com" {
			t.Errorf("base_url = %q, want the flag's value", cfg.BaseURL)
		}
		if cfg.Timeout != 13*time.Second {
			t.Errorf("timeout = %v, want 13s from the flag", cfg.Timeout)
		}
		if cfg.MaxRetries != 5 {
			t.Errorf("max_retries = %d, want 5 from the flag", cfg.MaxRetries)
		}
	})
}

// Zero is a meaningful value for retries, so the flag layer distinguishes
// "set to zero" from "not supplied" with a pointer rather than a sentinel.
func TestZeroRetriesCanBeSetExplicitly(t *testing.T) {
	isolate(t)
	zero := 0
	cfg, err := Load(Overrides{MaxRetries: &zero})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxRetries != 0 {
		t.Fatalf("max_retries = %d, want an explicit 0 to be honoured", cfg.MaxRetries)
	}
}

// An operator who points at a file and gets nothing is misconfigured.
// Silently ignoring that would hide the mistake until something else broke.
func TestExplicitlyNamedMissingFileIsAnError(t *testing.T) {
	isolate(t)
	t.Setenv(LocalConfigEnv, filepath.Join(t.TempDir(), "absent.toml"))

	_, err := Load(Overrides{})
	if err == nil {
		t.Fatal("Load() = nil, want an error for an explicitly named missing file")
	}
	if kind, _ := instinct.KindOf(err); kind != instinct.KindConfiguration {
		t.Fatalf("error kind = %q, want %q", kind, instinct.KindConfiguration)
	}
}

// An absent default file is normal, not an error.
func TestAbsentDefaultFileIsFine(t *testing.T) {
	isolate(t)
	if _, err := Load(Overrides{}); err != nil {
		t.Fatalf("Load() error = %v, want an absent default config to be fine", err)
	}
}

func TestValidationRejectsUnusableConfigurations(t *testing.T) {
	tests := []struct {
		name string
		ov   Overrides
		want string
	}{
		{
			"unknown provider",
			Overrides{Provider: "oracle"},
			"unknown provider",
		},
		{
			// The jev adapter has no default endpoint on purpose: defaulting
			// to a vendor's hostname would make a hosted service the
			// implicit destination of anyone's data.
			"jev without an endpoint",
			Overrides{Provider: ProviderJev},
			"needs a base_url",
		},
		{
			"non-positive timeout",
			Overrides{Timeout: -time.Second},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			_, err := Load(tc.ov)
			if tc.name == "non-positive timeout" {
				// A negative flag value is treated as unset rather than
				// invalid, so this must simply not take effect.
				if err != nil {
					t.Fatalf("Load() error = %v, want a negative timeout ignored", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
			if kind, _ := instinct.KindOf(err); kind != instinct.KindConfiguration {
				t.Fatalf("error kind = %q, want %q", kind, instinct.KindConfiguration)
			}
		})
	}
}

func TestMalformedFileIsReportedNotIgnored(t *testing.T) {
	isolate(t)
	t.Setenv(LocalConfigEnv, writeConfig(t, "this is not = = toml"))

	if _, err := Load(Overrides{}); err == nil {
		t.Fatal("Load() = nil, want a parse error rather than silently using defaults")
	}
}

func TestBadTimeoutInFileIsReported(t *testing.T) {
	isolate(t)
	t.Setenv(LocalConfigEnv, writeConfig(t, `timeout = "a fortnight"`))

	_, err := Load(Overrides{})
	if err == nil {
		t.Fatal("Load() = nil, want an error for an unparseable duration")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %q, want it to name the offending field", err)
	}
}

// The central rule: configuration names the variable holding a credential,
// never the credential. This makes a resolved configuration safe to print.
func TestConfigurationNeverCarriesTheCredentialItself(t *testing.T) {
	isolate(t)
	const secret = "sk-live-SUPERSECRET"
	t.Setenv("HOWLINSTINCT_TEST_KEY", secret)
	t.Setenv(LocalConfigEnv, writeConfig(t, `
provider = "jev"
base_url = "https://api.example.com"
api_key_env = "HOWLINSTINCT_TEST_KEY"
`))

	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIKeyEnv != "HOWLINSTINCT_TEST_KEY" {
		t.Fatalf("api_key_env = %q, want the variable name", cfg.APIKeyEnv)
	}

	described := cfg.Describe()
	if strings.Contains(described, secret) {
		t.Fatalf("Describe() leaked the credential:\n%s", described)
	}
	if !strings.Contains(described, "$HOWLINSTINCT_TEST_KEY") {
		t.Fatalf("Describe() should name the variable:\n%s", described)
	}
	if !strings.Contains(described, "(set)") {
		t.Fatalf("Describe() should report whether the variable is populated:\n%s", described)
	}

	// The adapter projection must carry the variable name, not the value.
	if jc := cfg.JevConfig(); jc.APIKeyEnv != "HOWLINSTINCT_TEST_KEY" {
		t.Fatalf("JevConfig().APIKeyEnv = %q, want the variable name", jc.APIKeyEnv)
	}
}

// An unset credential variable must be visible in the description, because
// that is the single most common misconfiguration.
func TestDescribeFlagsAnUnsetCredential(t *testing.T) {
	isolate(t)
	t.Setenv(LocalConfigEnv, writeConfig(t, `
provider = "jev"
base_url = "https://api.example.com"
api_key_env = "HOWLINSTINCT_DEFINITELY_UNSET"
`))
	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(cfg.Describe(), "UNSET") {
		t.Fatalf("Describe() did not flag the unset credential:\n%s", cfg.Describe())
	}
}

// The doctor command reports where configuration came from, which is the
// first question anyone asks when a value is not what they expected.
func TestSourcesAreRecorded(t *testing.T) {
	isolate(t)
	t.Setenv(LocalConfigEnv, writeConfig(t, `model = "m"`))
	t.Setenv(EnvModel, "n")

	cfg, err := Load(Overrides{Model: "o"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	joined := strings.Join(cfg.Sources, ",")
	for _, want := range []string{"defaults", "file:", "environment", "flags"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sources = %v, want it to record %q", cfg.Sources, want)
		}
	}
}
