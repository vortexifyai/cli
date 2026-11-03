package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWindowsCompletionWaitsAndCleansUp(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "update files")
	destination := filepath.Join(root, "installed bin")
	for _, dir := range []string{work, destination} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(work, "complete-update.ps1")
	if err := os.WriteFile(helper, []byte(windowsCompletionScript), 0o644); err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(root, "install.ps1")
	fake := `param([string]$BaseUrl, [string]$InstallDir)
$ErrorActionPreference = 'Stop'
$parent = Get-Process -Id ([int]$env:TASK_UPDATE_PARENT) -ErrorAction SilentlyContinue
if ($parent -and -not $parent.HasExited) { throw 'Installer ran before the parent exited.' }
if ($BaseUrl -ne 'https://github.com/vortexifyai/cli/releases/download/v0.1.1') { throw 'Wrong release.' }
Set-Content -LiteralPath (Join-Path $InstallDir 'installed.txt') -Value 'installed'
`
	if err := os.WriteFile(installer, []byte(fake), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 2")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
	pid := strconv.Itoa(parent.Process.Pid)
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", helper,
		"-ParentProcessId", pid, "-InstallerPath", installer, "-BaseUrl", downloadsURL+"v0.1.1", "-InstallDir", destination)
	command.Env = append(os.Environ(), "TASK_UPDATE_PARENT="+pid)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Windows completion failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(destination, "installed.txt")); err != nil {
		t.Fatal("installer did not run", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatal("update files were not cleaned up", err)
	}
}
