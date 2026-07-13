//go:build linux

package codexapp

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestConfigureChildProcessKillsAppServerWithParent(t *testing.T) {
	cmd := exec.Command("true")
	configureChildProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != unix.SIGKILL {
		t.Fatalf("Pdeathsig = %#v, want SIGKILL", cmd.SysProcAttr)
	}
}
