//go:build !linux

package codexapp

// There is no portable way to prove that an arbitrary non-Linux mount is
// memory-backed. Fail closed unless the explicit test-only option is used.
func isMemoryBacked(string) bool { return false }
