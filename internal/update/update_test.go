package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"v0.1.9", "v0.1.10", -1}, {"v0.9.0", "v0.10.0", -1},
		{"v2.0.0", "v1.99.99", 1}, {"0.1.0", "v0.1.0", 0},
	} {
		got, err := Compare(tc.left, tc.right)
		if err != nil || got != tc.want {
			t.Fatalf("Compare(%q, %q)=%d, %v", tc.left, tc.right, got, err)
		}
	}
	for _, value := range []string{"dev", "v1.0.0-beta.1", "v01.0.0", "v1.0", "v1.0.0/other", "v999999999999999999999999.0.0"} {
		if _, err := Compare(value, "v1.0.0"); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestLatest(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"stable", `{"tag_name":"v0.1.1","assets":[]}`, 200, false},
		{"draft", `{"tag_name":"v0.1.1","draft":true}`, 200, true},
		{"prerelease", `{"tag_name":"v0.1.1","prerelease":true}`, 200, true},
		{"invalid tag", `{"tag_name":"../../other"}`, 200, true},
		{"invalid JSON", `{`, 200, true},
		{"missing release", `{}`, 404, true},
		{"rate limit", `{}`, 403, true},
		{"server failure", `{}`, 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != latestURL || req.Method != "GET" || req.Header.Get("User-Agent") != "vortexify-cli" || req.Header.Get("Authorization") != "" {
					t.Fatal("unexpected GitHub request", req)
				}
				return response(tc.status, tc.body), nil
			})
			release, err := client.Latest(context.Background())
			if (err != nil) != tc.wantError {
				t.Fatalf("release=%+v, error=%v", release, err)
			}
		})
	}
}

func fixture(t *testing.T, goos string, script []byte, manifest string) (*Client, Release, string) {
	t.Helper()
	client := NewClient()
	client.goos, client.goarch = goos, "amd64"
	installer, executable := "install.sh", "vortexify"
	if goos == "windows" {
		installer, executable = "install.ps1", "vortexify.exe"
	}
	if manifest == "" {
		manifest = fmt.Sprintf("%x  %s\n", sha256.Sum256(script), installer)
	}
	client.HTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case downloadsURL + "v0.1.1/SHA256SUMS":
			return response(200, manifest), nil
		case downloadsURL + "v0.1.1/" + installer:
			return response(200, string(script)), nil
		default:
			t.Fatalf("unexpected download: %s", req.URL)
			return nil, nil
		}
	})
	binary := "vortexify-" + goos + "-amd64"
	if goos == "windows" {
		binary += ".exe"
	}
	release := Release{Tag: "v0.1.1", Assets: []Asset{{installer}, {"SHA256SUMS"}, {binary}}}
	dir := filepath.Join(t.TempDir(), "installed bin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, executable)
	if err := os.WriteFile(path, []byte("old CLI"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return client, release, path
}

func TestInstallRunsVerifiedInstallerWithPinnedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer execution")
	}
	log := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("TASK_UPDATE_ARGS", log)
	script := []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TASK_UPDATE_ARGS\"\n")
	client, release, path := fixture(t, "linux", script, "")
	link := filepath.Join(t.TempDir(), "cli-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	pending, err := client.Install(context.Background(), release, link, io.Discard, io.Discard)
	if err != nil || pending {
		t.Fatal(pending, err)
	}
	got, err := os.ReadFile(log)
	want := "--base-url\n" + release.BaseURL() + "\n--install-dir\n" + filepath.Dir(path) + "\n"
	if err != nil || string(got) != want {
		t.Fatalf("arguments=%q, want=%q, error=%v", got, want, err)
	}
}

func TestUntrustedInstallerIsNeverExecuted(t *testing.T) {
	for _, scenario := range []string{"mismatch", "missing", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			script := []byte("exit 0\n")
			manifest := fmt.Sprintf("%x  install.sh\n", sha256.Sum256(script))
			switch scenario {
			case "mismatch":
				script = []byte("tampered\n")
			case "missing":
				manifest = "no installer entry\n"
			case "duplicate":
				manifest += manifest
			}
			client, release, path := fixture(t, "linux", script, manifest)
			if _, err := client.Install(context.Background(), release, path, io.Discard, io.Discard); err == nil {
				t.Fatal("accepted untrusted installer")
			}
			data, _ := os.ReadFile(path)
			if string(data) != "old CLI" {
				t.Fatal("existing executable changed")
			}
		})
	}
}

func TestMissingAssetAndUnsupportedPlatform(t *testing.T) {
	client, release, path := fixture(t, "linux", []byte("exit 0\n"), "")
	release.Assets = nil
	if _, err := client.Install(context.Background(), release, path, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted missing assets")
	}
	client.goos = "unsupported"
	if _, err := client.Install(context.Background(), release, path, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted unsupported platform")
	}
}

func TestWindowsInstallerLaunchAndCleanup(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			client, release, path := fixture(t, "windows", []byte("# verified installer"), "")
			var workDir string
			client.launch = func(args []string) error {
				for index, arg := range args {
					if arg == "-File" {
						workDir = filepath.Dir(args[index+1])
					}
				}
				joined := strings.Join(args, "\n")
				if !strings.Contains(joined, "-ParentProcessId\n") || !strings.Contains(joined, release.BaseURL()) || !strings.Contains(joined, filepath.Dir(path)) {
					t.Fatal("missing Windows installer arguments", args)
				}
				if failed {
					return fmt.Errorf("launch failed")
				}
				return nil
			}
			pending, err := client.Install(context.Background(), release, path, io.Discard, io.Discard)
			if pending == failed || (err != nil) != failed {
				t.Fatal(pending, err)
			}
			_, statErr := os.Stat(workDir)
			if failed && !os.IsNotExist(statErr) || !failed && statErr != nil {
				t.Fatal("incorrect installer ownership/cleanup", statErr)
			}
			t.Cleanup(func() { _ = os.RemoveAll(workDir) })
		})
	}
}

func TestLatestHonorsCancellation(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Latest(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
}
