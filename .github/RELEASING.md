# CLI maintainer guide

## Build and test

```sh
cd /path/to/cli
go mod download
go test ./...
go test -race ./...
make build VERSION=v0.1.0
./dist/vortexify --version
./dist/vortexify --help
```

`make release VERSION=v0.1.0` builds macOS arm64/amd64, Linux arm64/amd64, and Windows amd64 binaries into `dist/` on the computer running that command. `GO` can override the Go executable. Building locally does not publish or upload anything. Only Go code and explicitly embedded assets become part of the executable; the installer remains a separate file.

Go finds `*_test.go` files when you run `go test ./...`. Unit tests live beside their packages; broader tests, including [shell installer tests](../tests/install_test.go) and [Windows installer tests](../tests/windows_install_test.go), live in `tests/`. The installer tests use temporary local release files to check platform selection, checksum verification, and preservation of an existing installation on failure. They do not download from GitHub. No Python test runner is needed for the installer.

## Publish a release

### One-time repository setup

Publish the contents of this `cli/` directory as a GitHub repository root. Keep `.github/`, `installation/`, `tests/`, the Go source, `Makefile`, `go.mod`, and `go.sum` together. Generated `dist/` files are ignored by Git. SDKs and agent skills are separate packages.

For the initial setup of [vortexifyai/cli](https://github.com/vortexifyai/cli), create an empty repository without generating a README, then run the following from this directory:

```sh
git init -b main
git add .
git diff --cached --stat
git commit -m "Initial CLI source and release workflow"
git remote add origin https://github.com/vortexifyai/cli.git
git push -u origin main
```

GitHub Actions must be enabled for the repository. Publication uses GitHub's automatically provided workflow token with `contents: write`; it does not require a personal access token or a separate upload script. The workflow uses the current repository automatically, so its name does not need to be hardcoded into the workflow or Go source.

### First release and later versions

Commit and push the source changes you want to release, then push a stable version tag:

```sh
git tag v0.1.0
git push origin v0.1.0
```

For later releases, use a new version such as `v0.1.1`. There is no separate CLI version file to edit: the Makefile embeds the tag's version into the executable during the build.

The tag triggers `.github/workflows/release.yml`. A Windows runner first tests the PowerShell installer and installs a real Windows executable. If that succeeds, an Ubuntu runner performs the release steps:

1. Runs `go test ./...`; a failure stops the release.
2. Runs `make release VERSION="$RELEASE_VERSION"`, producing five executables in `dist/` on the runner.
3. Writes release copies of `installation/install.sh` and `installation/install.ps1` into `dist/`, embedding the current release URL in each installer.
4. Calculates SHA-256 fingerprints and writes `dist/SHA256SUMS`.
5. Installs the built Linux executable into a temporary directory and checks `--version`.
6. Runs `gh release create` to upload the eight files below and generate release notes.

```text
dist/
  vortexify-darwin-arm64
  vortexify-darwin-amd64
  vortexify-linux-amd64
  vortexify-linux-arm64
  vortexify-windows-amd64.exe
  install.sh
  install.ps1
  SHA256SUMS
```

Open the repository's **Actions** tab to watch the run, then **Releases** to find the downloads. GitHub creates a URL for each uploaded file:

```text
https://github.com/vortexifyai/cli/releases/download/v0.1.0/install.sh
https://github.com/vortexifyai/cli/releases/download/v0.1.0/install.ps1
https://github.com/vortexifyai/cli/releases/download/v0.1.0/vortexify-darwin-arm64
https://github.com/vortexifyai/cli/releases/download/v0.1.0/SHA256SUMS
```

Published installers use their embedded `releases/download/v0.1.0` URL to download the matching executable and `SHA256SUMS`; users do not supply a URL. Source-checkout installers default to the latest release. Explicit `--base-url` / `-BaseUrl` overrides remain available for the CLI updater and other release hosts. See [installation instructions](../README.md#installation).

