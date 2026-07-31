//go:build linux

package codexapp

import "golang.org/x/sys/unix"

const ramfsMagic = 0x858458f6

func isMemoryBacked(path string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false
	}
	return uint64(stat.Type) == uint64(unix.TMPFS_MAGIC) || uint64(stat.Type) == ramfsMagic
}
