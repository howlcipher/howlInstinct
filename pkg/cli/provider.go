package cli

import (
	"github.com/howlcipher/howlinstinct/internal/config"
	"github.com/howlcipher/howlinstinct/internal/provider/jev"
	"github.com/howlcipher/howlinstinct/internal/provider/mock"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// This file is the composition root: the single place that knows which
// adapters exist and how to configure each one.
//
// Keeping the mapping here rather than in internal/config is deliberate.
// Configuration describes what an operator asked for in adapter-neutral
// terms; translating that into a particular adapter's settings is a wiring
// concern. Putting the translation in the config package would have made a
// layer that every provider depends on depend, in turn, on one of them.

// buildProvider constructs the adapter named by the resolved configuration.
func buildProvider(cfg config.Config) (instinct.Provider, error) {
	switch cfg.Provider {
	case config.ProviderMock:
		return mock.New(), nil
	case config.ProviderJev:
		return jev.New(jevConfig(cfg))
	default:
		return nil, instinct.Errorf(instinct.KindConfiguration, "provider",
			"unknown provider %q", cfg.Provider)
	}
}

// jevConfig projects adapter-neutral configuration onto the Jev adapter.
func jevConfig(cfg config.Config) jev.Config {
	out := jev.DefaultConfig()
	out.BaseURL = cfg.BaseURL
	out.Model = cfg.Model
	// The variable's NAME, never its value: the credential is read from the
	// environment at call time and never travels through configuration.
	out.APIKeyEnv = cfg.APIKeyEnv
	out.Timeout = cfg.Timeout
	out.MaxRetries = cfg.MaxRetries
	out.MaxResponseBytes = cfg.Limits.MaxResponseBytes
	if cfg.StrictSchema != nil {
		out.StrictSchema = *cfg.StrictSchema
	}
	return out
}

// providerEndpoint reports the credential-free endpoint an adapter will call,
// for provenance. The mock reaches nothing, so it reports nothing.
func providerEndpoint(p instinct.Provider) string {
	if j, ok := p.(*jev.Provider); ok {
		return j.Endpoint()
	}
	return ""
}
