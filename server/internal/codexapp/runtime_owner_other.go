//go:build !linux

package codexapp

import "os"

// Production auth material is supported only on Linux tmpfs. Other platforms
// can reach this helper solely through the explicit test-only runtime escape.
func runtimeDirOwnedByCurrentUser(os.FileInfo) bool { return true }
