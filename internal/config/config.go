// Package config resolves HowlInstinct's runtime configuration from layered
// sources, and is deliberate about one thing above all: a credential is never
// a configuration value.
//
// Configuration names the environment variable that holds a key. It never
// holds the key. That single rule is what lets a config file be committed, a
// resolved configuration be printed in a log or a bug report, and a doctor
// command be pasted into a ticket, all without anyone having to remember to
// redact something first.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// LocalConfigEnv points at an operator configuration file, overriding the
// default location.
const LocalConfigEnv = "HOWLINSTINCT_LOCAL_CONFIG"

// Environment variables recognized at the env layer.
const (
	EnvProvider   = "HOWLINSTINCT_PROVIDER"
	EnvBaseURL    = "HOWLINSTINCT_BASE_URL"
	EnvModel      = "HOWLINSTINCT_MODEL"
	EnvAPIKeyEnv  = "HOWLINSTINCT_API_KEY_ENV"
	EnvTimeout    = "HOWLINSTINCT_TIMEOUT"
	EnvMaxRetries = "HOWLINSTINCT_MAX_RETRIES"
)

// Provider names.
const (
	ProviderMock = "mock"
	ProviderJev  = "jev"
)

// Config is the resolved runtime configuration.
type Config struct {
	// Provider selects the adapter. It defaults to the mock provider so that
	// a fresh checkout works offline with no credentials.
	Provider string `toml:"provider"`

	// BaseURL, Model, and APIKeyEnv configure a Jev-compatible endpoint.
	// APIKeyEnv names a variable; it is never the key itself.
	BaseURL   string `toml:"base_url"`
	Model     string `toml:"model"`
	APIKeyEnv string `toml:"api_key_env"`

	Timeout    time.Duration `toml:"-"`
	TimeoutRaw string        `toml:"timeout"`
	MaxRetries int           `toml:"max_retries"`

	// StrictSchema rejects provider responses carrying unknown fields.
	StrictSchema *bool `toml:"strict_schema"`

	Limits instinct.Limits `toml:"-"`

	// Sources records which layers contributed, for the doctor command.
	Sources []string `toml:"-"`
}

// Default returns the code-level defaults: the bottom of the precedence
// stack, and a configuration that works with no file, no environment, and no
// network.
func Default() Config {
	return Config{
		Provider:   ProviderMock,
		Timeout:    20 * time.Second,
		MaxRetries: 2,
		Limits:     instinct.DefaultLimits(),
	}
}

// Overrides carries command-line values. A nil or empty field means the flag
// was not supplied, so it does not override anything.
type Overrides struct {
	Provider   string
	BaseURL    string
	Model      string
	APIKeyEnv  string
	Timeout    time.Duration
	MaxRetries *int
}

// Load resolves configuration in ascending order of authority:
//
//	code defaults  <  operator file  <  environment  <  command-line flags
//
// The rationale is that each layer is more specific to the moment than the
// one below it. A file describes a machine, the environment describes a
// process, and a flag describes this one invocation, so a flag has to be able
// to override everything for a single run without editing anything.
func Load(ov Overrides) (Config, error) {
	cfg := Default()
	cfg.Sources = append(cfg.Sources, "defaults")

	path, explicit := LocalConfigPath()
	if path != "" {
		fileCfg, found, err := loadFile(path)
		if err != nil {
			return cfg, err
		}
		switch {
		case found:
			cfg.mergeFile(fileCfg)
			cfg.Sources = append(cfg.Sources, "file:"+path)
		case explicit:
			// An operator who explicitly pointed at a file and got nothing
			// is misconfigured, and silently ignoring it would hide that.
			return cfg, instinct.Errorf(instinct.KindConfiguration, "config",
				"%s points at %q, which does not exist", LocalConfigEnv, path)
		}
	}

	if cfg.mergeEnv() {
		cfg.Sources = append(cfg.Sources, "environment")
	}
	if cfg.mergeOverrides(ov) {
		cfg.Sources = append(cfg.Sources, "flags")
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LocalConfigPath returns the operator configuration path and whether it was
// named explicitly rather than defaulted.
func LocalConfigPath() (path string, explicit bool) {
	if p := strings.TrimSpace(os.Getenv(LocalConfigEnv)); p != "" {
		return p, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".config", "howlinstinct", "config.toml"), false
}

// maxConfigBytes bounds the operator configuration file.
//
// The file is chosen by the operator rather than supplied by an attacker, so
// this is not a defence against a hostile input. It is a bound: a truncated
// disk, a wrong path pointing at something enormous, or a symlink to a device
// should fail with a clear message rather than read until memory runs out.
const maxConfigBytes = 1 << 20 // 1 MiB

func loadFile(path string) (Config, bool, error) {
	// #nosec G304 -- the path is the operator's own configuration file,
	// chosen by them, not untrusted input.
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, instinct.Wrap(instinct.KindConfiguration, "config", err,
			"opening %s", path)
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Config{}, false, instinct.Wrap(instinct.KindConfiguration, "config", err,
			"reading %s", path)
	}
	if len(raw) > maxConfigBytes {
		return Config{}, false, instinct.Errorf(instinct.KindConfiguration, "config",
			"%s exceeds the %d byte configuration limit", path, maxConfigBytes)
	}
	var out Config
	if err := toml.Unmarshal(raw, &out); err != nil {
		return Config{}, false, instinct.Wrap(instinct.KindConfiguration, "config", err,
			"parsing %s", path)
	}
	if out.TimeoutRaw != "" {
		d, err := time.ParseDuration(out.TimeoutRaw)
		if err != nil {
			return Config{}, false, instinct.Wrap(instinct.KindConfiguration, "config", err,
				"parsing timeout %q in %s", out.TimeoutRaw, path)
		}
		out.Timeout = d
	}
	return out, true, nil
}

func (c *Config) mergeFile(f Config) {
	setString(&c.Provider, f.Provider)
	setString(&c.BaseURL, f.BaseURL)
	setString(&c.Model, f.Model)
	setString(&c.APIKeyEnv, f.APIKeyEnv)
	if f.Timeout > 0 {
		c.Timeout = f.Timeout
	}
	if f.MaxRetries > 0 {
		c.MaxRetries = f.MaxRetries
	}
	if f.StrictSchema != nil {
		c.StrictSchema = f.StrictSchema
	}
}

func (c *Config) mergeEnv() bool {
	var touched bool
	for _, m := range []struct {
		env string
		dst *string
	}{
		{EnvProvider, &c.Provider},
		{EnvBaseURL, &c.BaseURL},
		{EnvModel, &c.Model},
		{EnvAPIKeyEnv, &c.APIKeyEnv},
	} {
		if v := strings.TrimSpace(os.Getenv(m.env)); v != "" {
			*m.dst = v
			touched = true
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvTimeout)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.Timeout = d
			touched = true
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvMaxRetries)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.MaxRetries = n
			touched = true
		}
	}
	return touched
}

func (c *Config) mergeOverrides(ov Overrides) bool {
	var touched bool
	for _, m := range []struct {
		val string
		dst *string
	}{
		{ov.Provider, &c.Provider},
		{ov.BaseURL, &c.BaseURL},
		{ov.Model, &c.Model},
		{ov.APIKeyEnv, &c.APIKeyEnv},
	} {
		if m.val != "" {
			*m.dst = m.val
			touched = true
		}
	}
	if ov.Timeout > 0 {
		c.Timeout = ov.Timeout
		touched = true
	}
	if ov.MaxRetries != nil {
		c.MaxRetries = *ov.MaxRetries
		touched = true
	}
	return touched
}

func setString(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// Validate checks the resolved configuration for internal consistency.
func (c Config) Validate() error {
	switch c.Provider {
	case ProviderMock:
	case ProviderJev:
		if c.BaseURL == "" {
			return instinct.Errorf(instinct.KindConfiguration, "config",
				"provider %q needs a base_url; set it in %s, %s, or --base-url",
				c.Provider, configFileHint(), EnvBaseURL)
		}
	default:
		return instinct.Errorf(instinct.KindConfiguration, "config",
			"unknown provider %q; known providers are %q and %q",
			c.Provider, ProviderMock, ProviderJev)
	}
	if c.Timeout <= 0 {
		return instinct.Errorf(instinct.KindConfiguration, "config",
			"timeout must be positive, got %s", c.Timeout)
	}
	if c.MaxRetries < 0 {
		return instinct.Errorf(instinct.KindConfiguration, "config",
			"max_retries must not be negative, got %d", c.MaxRetries)
	}
	return nil
}

func configFileHint() string {
	p, _ := LocalConfigPath()
	if p == "" {
		return "the operator config file"
	}
	return p
}

// Describe renders the resolved configuration for humans.
//
// It is safe to print anywhere: the only credential-adjacent value it holds
// is the NAME of an environment variable, and that is what it prints.
func (c Config) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider:      %s\n", c.Provider)
	if c.Provider == ProviderJev {
		fmt.Fprintf(&b, "base_url:      %s\n", c.BaseURL)
		fmt.Fprintf(&b, "model:         %s\n", orNone(c.Model))
		fmt.Fprintf(&b, "credential:    %s\n", c.describeCredential())
	}
	fmt.Fprintf(&b, "timeout:       %s\n", c.Timeout)
	fmt.Fprintf(&b, "max_retries:   %d\n", c.MaxRetries)
	fmt.Fprintf(&b, "max_questions: %d\n", c.Limits.MaxQuestions)
	fmt.Fprintf(&b, "max_state:     %d bytes\n", c.Limits.MaxStateBytes)
	fmt.Fprintf(&b, "sources:       %s\n", strings.Join(c.Sources, " < "))
	return b.String()
}

func (c Config) describeCredential() string {
	if c.APIKeyEnv == "" {
		return "none configured (the endpoint is expected to be unauthenticated)"
	}
	if os.Getenv(c.APIKeyEnv) == "" {
		return "$" + c.APIKeyEnv + " (UNSET)"
	}
	return "$" + c.APIKeyEnv + " (set)"
}

func orNone(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}
