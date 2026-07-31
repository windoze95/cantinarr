//go:build linux

package codexapp

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &unix.SysProcAttr{Pdeathsig: unix.SIGKILL}
}
