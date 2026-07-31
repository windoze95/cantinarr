//go:build !linux

package codexapp

import "os/exec"

func configureChildProcess(*exec.Cmd) {}
