// Package version exposes build metadata stamped at link time.
package version

import "runtime/debug"

// version is overridden at build time via:
//
//	-ldflags "-X github.com/ratyx/remedik/internal/version.version=v0.1.0"
var version = "dev"

// String returns the human-readable version, falling back to the VCS
// revision embedded by the Go toolchain when no explicit version was
// stamped.
func String() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				return "dev+" + s.Value[:12]
			}
		}
	}
	return version
}
