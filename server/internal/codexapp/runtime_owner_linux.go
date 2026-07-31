//go:build linux

package codexapp

import (
	"os"
	"syscall"
)

func runtimeDirOwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
