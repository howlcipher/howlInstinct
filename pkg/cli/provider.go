package cli

import (
	"github.com/howlcipher/howlinstinct/internal/config"
	"github.com/howlcipher/howlinstinct/internal/provider/jev"
	"github.com/howlcipher/howlinstinct/internal/provider/mock"
	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// buildProvider constructs the adapter named by the resolved configuration.
func buildProvider(cfg config.Config) (instinct.Provider, error) {
	switch cfg.Provider {
	case config.ProviderMock:
		return mock.New(), nil
	case config.ProviderJev:
		return jev.New(cfg.JevConfig())
	default:
		return nil, instinct.Errorf(instinct.KindConfiguration, "provider",
			"unknown provider %q", cfg.Provider)
	}
}

// providerEndpoint reports the credential-free endpoint an adapter will call,
// for provenance. The mock reaches nothing, so it reports nothing.
func providerEndpoint(p instinct.Provider) string {
	if j, ok := p.(*jev.Provider); ok {
		return j.Endpoint()
	}
	return ""
}
