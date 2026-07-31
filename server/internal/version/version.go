// Package version exposes the build-time version of the Cantinarr server.
package version

// Version is the running server version. It defaults to "dev" for local and
// untagged builds and is overridden at build time via:
//
//	-ldflags "-X github.com/windoze95/cantinarr-server/internal/version.Version=$VERSION"
//
// Release images bake the git tag here so the server can report its own version
// and compare it against the latest published GitHub release.
var Version = "dev"
