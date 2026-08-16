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

// MinAppVersion is the oldest app build this server still fully supports,
// advertised to clients in /api/config as min_app_version. Clients below it
// show a warn-only "update this app" banner — never a block; a hard block
// would be a deliberate future escalation. "0.0.0" means no floor. Twin
// constant: minServerVersion in app/lib/core/utils/version_compat.dart —
// raise either only alongside the breaking change that forces it.
const MinAppVersion = "0.0.0"
