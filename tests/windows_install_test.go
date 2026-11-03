package tests

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func windowsInstallerCommand(t *testing.T, script string, args ...string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows PowerShell; exercised by the release workflow")
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(powershell, append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass"}, args...)...)
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if key == "VORTEXIFY_DOWNLOAD_BASE_URL" || key == "VORTEXIFY_INSTALL_DIR" || key == "PROCESSOR_ARCHITECTURE" || key == "PROCESSOR_ARCHITEW6432" || strings.HasPrefix(key, "TASK_INSTALL_") {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "PROCESSOR_ARCHITECTURE=AMD64", "TASK_INSTALL_SCRIPT="+script)
	return cmd
}

func TestWindowsInstaller(t *testing.T) {
	for _, tc := range []struct {
		name, problem, wantError string
		existing                 bool
	}{
		{"initial install", "", "", false},
		{"replace installation", "", "", true},
		{"corrupt download", "corrupt", "Checksum mismatch", true},
		{"duplicate checksum", "duplicate", "exactly one", true},
		{"missing checksum", "missing-checksum", "exactly one", true},
		{"empty executable", "empty", "executable is empty", true},
		{"conflicting sources", "both-sources", "not both", true},
		{"insecure URL", "http", "must use HTTPS", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS != "windows" {
				t.Skip("requires Windows")
			}
			root := t.TempDir()
			release := filepath.Join(root, "release with spaces")
			destination := filepath.Join(root, "[bin] with spaces")
			for _, dir := range []string{release, destination} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path string, value []byte) {
				t.Helper()
				if err := os.WriteFile(path, value, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			binary := []byte("verified release")
			if tc.problem == "empty" {
				binary = nil
			}
			checksum := fmt.Sprintf("%x  vortexify-windows-amd64.exe\n", sha256.Sum256(binary))
			if tc.problem == "duplicate" {
				checksum += checksum
			}
			if tc.problem == "missing-checksum" {
				checksum = ""
			}
			write(filepath.Join(release, "SHA256SUMS"), []byte(checksum))
			if tc.problem == "corrupt" {
				binary = []byte("corrupted")
			}
			write(filepath.Join(release, "vortexify-windows-amd64.exe"), binary)
			target := filepath.Join(destination, "vortexify.exe")
			if tc.existing {
				write(target, []byte("previous"))
			}
			script, err := filepath.Abs(filepath.Join("..", "installation", "install.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-File", script, "-InstallDir", destination, "-NoPathUpdate"}
			switch tc.problem {
			case "both-sources":
				args = append(args, "-FromDir", release, "-BaseUrl", "https://example.invalid/release")
			case "http":
				args = append(args, "-BaseUrl", "http://example.invalid/release")
			default:
				args = append(args, "-FromDir", release)
			}
			output, err := windowsInstallerCommand(t, script, args...).CombinedOutput()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(string(output), tc.wantError) {
					t.Fatalf("expected %q; error=%v, output=%s", tc.wantError, err, output)
				}
			} else if err != nil {
				t.Fatalf("installer failed: %v\n%s", err, output)
			}
			want := "verified release"
			if tc.wantError != "" {
				want = "previous"
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != want {
				t.Fatalf("installed content=%q, want=%q, error=%v", got, want, err)
			}
			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != 1 {
				t.Fatalf("unexpected staged files: %v, %v", entries, err)
			}
		})
	}
}

func TestWindowsInstallerUserPath(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "installation", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := []byte("verified release")
	if err := os.WriteFile(filepath.Join(root, "vortexify-windows-amd64.exe"), binary, 0o644); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  vortexify-windows-amd64.exe\n", sha256.Sum256(binary))
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}
	// Save and restore the runner's user PATH even if an assertion fails.
	command := `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 0
$original = [Environment]::GetEnvironmentVariable('Path', 'User')
$destination = Join-Path $env:TASK_INSTALL_RELEASE 'installed bin'
try {
    & $env:TASK_INSTALL_SCRIPT -FromDir $env:TASK_INSTALL_RELEASE -InstallDir $destination
    if ($LASTEXITCODE -ne 0) { throw 'First install failed.' }
    & $env:TASK_INSTALL_SCRIPT -FromDir $env:TASK_INSTALL_RELEASE -InstallDir $destination
    if ($LASTEXITCODE -ne 0) { throw 'Second install failed.' }
    $actual = [Environment]::GetEnvironmentVariable('Path', 'User')
    $expected = if ($original) { $original.TrimEnd(';') + ';' + $destination } else { $destination }
    if ($actual -ne $expected) { throw 'User PATH was duplicated or existing entries changed.' }
} finally {
    [Environment]::SetEnvironmentVariable('Path', $original, 'User')
}

`
	cmd := windowsInstallerCommand(t, script, "-Command", command)
	cmd.Env = append(cmd.Env, "TASK_INSTALL_RELEASE="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PATH registration failed: %v\n%s", err, output)
	}
}

func TestWindowsInstallerDownload(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "installation", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := []byte("verified release")
	if err := os.WriteFile(filepath.Join(root, "vortexify-windows-amd64.exe"), binary, 0o644); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  vortexify-windows-amd64.exe\n", sha256.Sum256(binary))
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}
	// Replace only the HTTP operation; exercise normal URL selection and installation.
	command := `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 0
function global:Invoke-WebRequest {
    param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)
    $name = ([Uri]$Uri).Segments[-1]
    if ($Uri -ne ('https://github.com/vortexifyai/cli/releases/latest/download/' + $name)) { throw 'Unexpected download URL.' }
    Add-Content -LiteralPath (Join-Path $env:TASK_INSTALL_RELEASE 'requests.txt') -Value $name
    Copy-Item -LiteralPath (Join-Path $env:TASK_INSTALL_RELEASE $name) -Destination $OutFile
}
& $env:TASK_INSTALL_SCRIPT -InstallDir (Join-Path $env:TASK_INSTALL_RELEASE 'downloaded bin') -NoPathUpdate
exit $LASTEXITCODE
`
	cmd := windowsInstallerCommand(t, script, "-Command", command)
	cmd.Env = append(cmd.Env, "TASK_INSTALL_RELEASE="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("download installation failed: %v\n%s", err, output)
	}
	got, err := os.ReadFile(filepath.Join(root, "downloaded bin", "vortexify.exe"))
	if err != nil || string(got) != string(binary) {
		t.Fatalf("downloaded executable=%q, error=%v", got, err)
	}
	requests, err := os.ReadFile(filepath.Join(root, "requests.txt"))
	if err != nil || strings.Join(strings.Fields(string(requests)), ",") != "SHA256SUMS,vortexify-windows-amd64.exe" {
		t.Fatalf("unexpected downloads: %q, error=%v", requests, err)
	}
}
