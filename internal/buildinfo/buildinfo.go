// Package buildinfo exposes the build version, injected at link time.
package buildinfo

// Version is the build version reported in /healthz responses and startup
// logs. It defaults to "dev" for local or un-tagged builds and is overridden
// at release time via:
//
//	-ldflags "-X github.com/kahz12/droidmcp/internal/buildinfo.Version=<v>"
//
// The Makefile, scripts/build-arm64.sh and the release workflow all inject the
// same value (the git tag) so binaries built from one commit agree.
var Version = "dev"
