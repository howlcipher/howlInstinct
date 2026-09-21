// Package instinct is the public, provider-neutral API of HowlInstinct.
//
// HowlInstinct answers small, bounded questions about supplied state and
// returns typed judgments carrying explicit uncertainty and provenance. It is
// not an agent: nothing in this package plans, executes, retries a workflow,
// or authorizes an action.
//
// The boundary this package exists to protect is that confidence is not
// permission. A Judgment reports what a provider believed and how strongly;
// deciding what may happen as a result belongs to the caller (in the Howl
// ecosystem, to HowlFrame). Consequently this package contains no threshold
// constant, no default cutoff, and no branch from a probability to an action.
//
// Nothing in this package may name a vendor, a model, an endpoint, or a wire
// format. Those concepts live only in the provider adapters under
// internal/provider.
package instinct
