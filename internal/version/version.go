// Package version exposes the build version of the patchr binary.
package version

// Version is the build version. It defaults to "dev" and is overridden at
// build time via -ldflags "-X .../internal/version.Version=<v>" (see Makefile).
var Version = "dev"
