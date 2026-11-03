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

func TestInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shell installer supports macOS and Linux")
	}
	for _, tc := range []struct {
		name, system, arch, asset, problem, wantError string
	}{
		{"linux arm64", "Linux", "aarch64", "vortexify-linux-arm64", "", ""},
		{"macOS amd64", "Darwin", "x86_64", "vortexify-darwin-amd64", "", ""},
		{"corrupt download", "Linux", "aarch64", "vortexify-linux-arm64", "corrupt", "checksum mismatch"},
		{"duplicate checksum", "Linux", "aarch64", "vortexify-linux-arm64", "duplicate", "exactly one"},
		{"unsupported system", "Unsupported", "aarch64", "vortexify-linux-arm64", "", "unsupported operating system"},
		{"insecure URL", "Linux", "aarch64", "vortexify-linux-arm64", "http", "must use HTTPS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			release := filepath.Join(root, "release with spaces")
			destination := filepath.Join(root, "bin with spaces")
			shims := filepath.Join(root, "shims")
			for _, dir := range []string{release, destination, shims} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path string, content []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, content, mode); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(shims, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) printf '%s\\n' \"$TASK_TEST_SYSTEM\" ;; -m) printf '%s\\n' \"$TASK_TEST_ARCH\" ;; esac\n"), 0o755)
			binary := []byte("verified release")
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), tc.asset)
			if tc.problem == "duplicate" {
				checksum += checksum
			}
			write(filepath.Join(release, "SHA256SUMS"), []byte(checksum), 0o644)
			if tc.problem == "corrupt" {
				binary = []byte("corrupted")
			}
			write(filepath.Join(release, tc.asset), binary, 0o644)
			target := filepath.Join(destination, "vortexify")
			write(target, []byte("previous"), 0o755)
			args := []string{filepath.Join("..", "installation", "install.sh"), "--install-dir", destination}
			if tc.problem == "http" {
				args = append(args, "--base-url", "http://example.invalid/release")
			} else {
				args = append(args, "--from-dir", release)
			}
			cmd := exec.Command("sh", args...)
			for _, value := range os.Environ() {
				if strings.HasPrefix(value, "VORTEXIFY_DOWNLOAD_BASE_URL=") || strings.HasPrefix(value, "VORTEXIFY_INSTALL_DIR=") || strings.HasPrefix(value, "PATH=") || strings.HasPrefix(value, "TASK_TEST_SYSTEM=") || strings.HasPrefix(value, "TASK_TEST_ARCH=") {
					continue
				}
				cmd.Env = append(cmd.Env, value)
			}
			cmd.Env = append(cmd.Env, "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"), "TASK_TEST_SYSTEM="+tc.system, "TASK_TEST_ARCH="+tc.arch)
			output, err := cmd.CombinedOutput()
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
			info, err := os.Stat(target)
			if err != nil || info.Mode()&0o111 == 0 {
				t.Fatalf("installed file is not executable: %v", err)
			}
			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != 1 {
				t.Fatalf("installer left unexpected files: %v, %v", entries, err)
			}
		})
	}
}

func TestInstallerDefaultReleaseDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	original, err := os.ReadFile(filepath.Join("..", "installation", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stamped := range []bool{false, true} {
		t.Run(fmt.Sprint(stamped), func(t *testing.T) {
			root := t.TempDir()
			release := filepath.Join(root, "release")
			shims := filepath.Join(root, "shims")
			destination := filepath.Join(root, "installed bin")
			for _, dir := range []string{release, shims} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path string, content []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, content, mode); err != nil {
					t.Fatal(err)
				}
			}
			base := "https://github.com/vortexifyai/cli/releases/latest/download"
			script := string(original)
			if stamped {
				pinned := "https://github.com/vortexifyai/cli/releases/download/v0.1.0"
				script = strings.ReplaceAll(script, base, pinned)
				base = pinned
			}
			path := filepath.Join(root, "install.sh")
			write(path, []byte(script), 0o644)
			binary := []byte("verified release")
			asset := "vortexify-" + runtime.GOOS + "-" + runtime.GOARCH
			write(filepath.Join(release, asset), binary, 0o644)
			write(filepath.Join(release, "SHA256SUMS"), []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), asset)), 0o644)
			write(filepath.Join(shims, "curl"), []byte(`#!/bin/sh
set -eu
url=
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    https://*) url=$1 ;;
    -o) shift; output=$1 ;;
  esac
  shift
done
case "$url" in "$TASK_EXPECTED_BASE/"*) ;; *) exit 2 ;; esac
cp "$TASK_TEST_RELEASE/${url##*/}" "$output"
`), 0o755)
			cmd := exec.Command("sh", path, "--install-dir", destination)
			for _, value := range os.Environ() {
				if strings.HasPrefix(value, "VORTEXIFY_DOWNLOAD_BASE_URL=") || strings.HasPrefix(value, "VORTEXIFY_INSTALL_DIR=") || strings.HasPrefix(value, "PATH=") || strings.HasPrefix(value, "TASK_TEST_RELEASE=") || strings.HasPrefix(value, "TASK_EXPECTED_BASE=") {
					continue
				}
				cmd.Env = append(cmd.Env, value)
			}
			cmd.Env = append(cmd.Env, "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"), "TASK_TEST_RELEASE="+release, "TASK_EXPECTED_BASE="+base)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("default install failed: %v\n%s", err, output)
			}
			got, err := os.ReadFile(filepath.Join(destination, "vortexify"))
			if err != nil || string(got) != string(binary) {
				t.Fatal(string(got), err)
			}
		})
	}
}
