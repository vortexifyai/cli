//go:build windows

package dev

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {}
func terminateProcess(cmd *exec.Cmd, force bool) {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
}

func npmCommand(npm string, port int) *exec.Cmd {
	args := []string{"run", "dev", "--", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--strictPort"}
	if strings.EqualFold(filepath.Ext(npm), ".cmd") || strings.EqualFold(filepath.Ext(npm), ".bat") {
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c ""` + npm + `" ` + strings.Join(args, " ") + `"`}
		return cmd
	}

	return exec.Command(npm, args...)
}
