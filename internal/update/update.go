// Package update checks GitHub releases and installs them with the published installer.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const latestURL = "https://api.github.com/repos/vortexifyai/cli/releases/latest"
const downloadsURL = "https://github.com/vortexifyai/cli/releases/download/"

var stableVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Asset struct {
	Name string `json:"name"`
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

func (r Release) BaseURL() string { return downloadsURL + r.Tag }

type Client struct {
	HTTP         *http.Client
	goos, goarch string
	launch       func([]string) error
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 10 {
				return fmt.Errorf("invalid release download redirect")
			}
			return nil
		}},
		goos: runtime.GOOS, goarch: runtime.GOARCH, launch: launchWindowsInstaller,
	}
}

// Compare compares stable semantic versions numerically, accepting an optional v prefix.
func Compare(left, right string) (int, error) {
	parse := func(value string) ([3]uint64, error) {
		var parts [3]uint64
		matches := stableVersion.FindStringSubmatch(value)
		if matches == nil {
			return parts, fmt.Errorf("%q is not a stable release version", value)
		}
		for index := range parts {
			part, err := strconv.ParseUint(matches[index+1], 10, 64)
			if err != nil {
				return parts, fmt.Errorf("invalid version %q: %w", value, err)
			}
			parts[index] = part
		}
		return parts, nil
	}
	a, err := parse(left)
	if err != nil {
		return 0, err
	}
	b, err := parse(right)
	if err != nil {
		return 0, err
	}
	for index := range a {
		if a[index] < b[index] {
			return -1, nil
		}
		if a[index] > b[index] {
			return 1, nil
		}
	}
	return 0, nil
}

func (c *Client) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vortexify-cli")
	if address == latestURL {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release or download not found on GitHub")
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub denied the update request or its rate limit was reached; try again later")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GitHub response exceeds the size limit")
	}
	return data, nil
}

func validateRelease(release Release) error {
	if release.Draft || release.Prerelease || !strings.HasPrefix(release.Tag, "v") {
		return fmt.Errorf("GitHub did not return a stable published release")
	}
	_, err := Compare(release.Tag, release.Tag)
	return err
}

func (c *Client) Latest(ctx context.Context) (Release, error) {
	var release Release
	data, err := c.get(ctx, latestURL, 4<<20)
	if err != nil {
		return release, err
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return release, fmt.Errorf("invalid GitHub release response: %w", err)
	}
	return release, validateRelease(release)
}

func verifyInstaller(manifest []byte, name string, script []byte) error {
	var expected string
	count := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			expected = fields[0]
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("release must contain exactly one checksum for %s", name)
	}
	hash, err := hex.DecodeString(expected)
	actual := sha256.Sum256(script)
	if err != nil || len(hash) != len(actual) || !strings.EqualFold(expected, hex.EncodeToString(actual[:])) || len(script) == 0 {
		return fmt.Errorf("installer checksum verification failed; the CLI was not changed")
	}
	return nil
}

// Install runs the verified release installer in the current executable's directory.
// On Windows it starts a separate installer window, which waits for this process to exit.
func (c *Client) Install(ctx context.Context, release Release, executable string, stdout, stderr io.Writer) (pending bool, err error) {
	if err := validateRelease(release); err != nil {
		return false, err
	}
	installer := "install.sh"
	if c.goos == "windows" && c.goarch == "amd64" {
		installer = "install.ps1"
	} else if (c.goos != "linux" && c.goos != "darwin") || (c.goarch != "amd64" && c.goarch != "arm64") {
		return false, fmt.Errorf("updates are unsupported on %s/%s", c.goos, c.goarch)
	}
	binaryName := fmt.Sprintf("vortexify-%s-%s", c.goos, c.goarch)
	executableName := "vortexify"
	if c.goos == "windows" {
		binaryName += ".exe"
		executableName += ".exe"
	}
	for _, required := range []string{installer, "SHA256SUMS", binaryName} {
		count := 0
		for _, asset := range release.Assets {
			if asset.Name == required {
				count++
			}
		}
		if count != 1 {
			return false, fmt.Errorf("release %s does not include %s", release.Tag, required)
		}
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return false, fmt.Errorf("could not locate the installed CLI: %w", err)
	}
	if filepath.Base(executable) != executableName {
		return false, fmt.Errorf("the installed executable must be named %s; use the published installer to install it", executableName)
	}
	installDir := filepath.Dir(executable)
	manifest, err := c.get(ctx, release.BaseURL()+"/SHA256SUMS", 1<<20)
	if err != nil {
		return false, err
	}
	script, err := c.get(ctx, release.BaseURL()+"/"+installer, 1<<20)
	if err != nil {
		return false, err
	}
	if err := verifyInstaller(manifest, installer, script); err != nil {
		return false, err
	}
	workDir, err := os.MkdirTemp("", "vortexify-update-")
	if err != nil {
		return false, err
	}
	defer func() {
		if !pending {
			_ = os.RemoveAll(workDir)
		}
	}()
	path := filepath.Join(workDir, installer)
	if err := os.WriteFile(path, script, 0o600); err != nil {
		return false, err
	}
	if c.goos == "windows" {
		helper := filepath.Join(workDir, "complete-update.ps1")
		if err := os.WriteFile(helper, []byte(windowsCompletionScript), 0o600); err != nil {
			return false, err
		}
		args := []string{"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-NoExit", "-File", helper,
			"-ParentProcessId", strconv.Itoa(os.Getpid()), "-InstallerPath", path, "-BaseUrl", release.BaseURL(), "-InstallDir", installDir}
		if err := c.launch(args); err != nil {
			return false, fmt.Errorf("could not start the Windows installer: %w", err)
		}
		return true, nil
	}
	command := exec.CommandContext(ctx, "sh", path, "--base-url", release.BaseURL(), "--install-dir", installDir)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return false, fmt.Errorf("update installer failed: %w", err)
	}
	return false, nil
}

const windowsCompletionScript = `param([int]$ParentProcessId, [string]$InstallerPath, [string]$BaseUrl, [string]$InstallDir)
$ErrorActionPreference = 'Stop'
try {
    $parent = Get-Process -Id $ParentProcessId -ErrorAction SilentlyContinue
    if ($parent -and -not $parent.WaitForExit(120000)) { throw 'The CLI is still running. Close it and run the updater again.' }
    & (Join-Path $PSHOME 'powershell.exe') -NoProfile -ExecutionPolicy Bypass -File $InstallerPath -BaseUrl $BaseUrl -InstallDir $InstallDir
    if ($LASTEXITCODE -ne 0) { throw 'The installer failed. Your CLI may still be on its previous version.' }
    Write-Host 'Vortexify updated. Run vortexify --version in your terminal to confirm.'
} catch {
    Write-Host "Vortexify update failed: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
} finally {
    Remove-Item -LiteralPath $PSScriptRoot -Recurse -Force -ErrorAction SilentlyContinue
}
`
