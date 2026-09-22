// Package version carries the build's version string.
package version

// Version is overridden at build time with:
//
//	-ldflags "-X github.com/howlcipher/howlinstinct/internal/version.Version=v1.2.3"
//
// The default names an unreleased build explicitly, so that a binary built
// outside the release pipeline never claims to be a release.
var Version = "0.0.0-dev"
