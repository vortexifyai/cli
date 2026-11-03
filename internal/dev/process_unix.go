//go:build !windows

package dev

import (
	"os/exec"
	"strconv"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func terminateProcess(cmd *exec.Cmd, force bool) {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, signal)
}

func npmCommand(npm string, port int) *exec.Cmd {
	return exec.Command(npm, "run", "dev", "--", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--strictPort")
}
