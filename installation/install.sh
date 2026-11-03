#!/bin/sh
# Install a checksummed Vortexify release binary on macOS or Linux.
set -eu

usage() {
    cat <<'HELP'
Usage: sh install.sh [--base-url HTTPS_URL | --from-dir DIRECTORY] [--install-dir DIRECTORY]

  --base-url URL       Published release directory containing binaries and SHA256SUMS
  --from-dir DIR       Install from a local release directory instead of downloading
  --install-dir DIR    Destination directory (default: ~/.local/bin)
  -h, --help          Show this help

VORTEXIFY_DOWNLOAD_BASE_URL and VORTEXIFY_INSTALL_DIR provide optional defaults.
Downloads from the Vortexify CLI GitHub release by default. Windows users should
use install.ps1; this script supports macOS and Linux only.
HELP
}
fail() { printf 'vortexify installer: %s\n' "$*" >&2; exit 1; }
base_url=${VORTEXIFY_DOWNLOAD_BASE_URL:-}
source_dir=
install_dir=${VORTEXIFY_INSTALL_DIR:-"$HOME/.local/bin"}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --base-url|--from-dir|--install-dir)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 requires a value"
            case "$1" in
                --base-url) base_url=$2 ;;
                --from-dir) source_dir=$2 ;;
                --install-dir) install_dir=$2 ;;
            esac
            shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
[ -z "$source_dir" ] || [ -z "$base_url" ] || fail 'choose --base-url or --from-dir, not both'
if [ -z "$source_dir" ] && [ -z "$base_url" ]; then
    base_url='https://github.com/vortexifyai/cli/releases/latest/download'
fi
case "$base_url" in ''|https://*) ;; *) fail '--base-url must use HTTPS' ;; esac
case "$(uname -s)" in
    Darwin) platform=darwin ;;
    Linux) platform=linux ;;
    *) fail 'unsupported operating system; use its standalone release binary' ;;
esac
case "$(uname -m)" in
    arm64|aarch64) architecture=arm64 ;;
    x86_64|amd64) architecture=amd64 ;;
    *) fail 'unsupported architecture; available architectures are amd64 and arm64' ;;
esac
artifact="vortexify-$platform-$architecture"
task_tmp=$(mktemp -d "${TMPDIR:-/tmp}/vortexify-install.XXXXXX")
staged_binary=
cleanup() {
    rm -rf "$task_tmp"
    if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget --https-only -q -O "$2" "$1"
    else
        fail 'curl or wget is required for downloads'
    fi
}
if [ -n "$source_dir" ]; then
    cp "$source_dir/SHA256SUMS" "$task_tmp/SHA256SUMS" || fail 'could not read release checksums'
    cp "$source_dir/$artifact" "$task_tmp/$artifact" || fail "could not read $artifact"
else
    fetch "${base_url%/}/SHA256SUMS" "$task_tmp/SHA256SUMS" || fail 'could not download release checksums'
    fetch "${base_url%/}/$artifact" "$task_tmp/$artifact" || fail "could not download $artifact"
fi
expected=$(awk -v asset="$artifact" '$2 == asset || $2 == "*" asset {print $1}' "$task_tmp/SHA256SUMS")
[ "${#expected}" -eq 64 ] || fail 'release must contain exactly one SHA-256 checksum for this binary'
case "$expected" in *[!0-9a-fA-F]*) fail 'invalid SHA-256 checksum' ;; esac
if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$task_tmp/$artifact" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$task_tmp/$artifact" | awk '{print $1}')
else
    fail 'sha256sum or shasum is required'
fi
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
[ "$actual" = "$expected" ] || fail 'checksum mismatch; the installed CLI was not changed'
[ -s "$task_tmp/$artifact" ] || fail 'release binary is empty'
mkdir -p "$install_dir"
staged_binary=$(mktemp "$install_dir/.vortexify.XXXXXX")
cp "$task_tmp/$artifact" "$staged_binary"
chmod 755 "$staged_binary"
mv -f "$staged_binary" "$install_dir/vortexify"
staged_binary=
printf 'Installed Vortexify to %s/vortexify\n' "$install_dir"
case ":${PATH:-}:" in
    *":$install_dir:"*) ;;
    *) printf 'Add this directory to your shell PATH: %s\n' "$install_dir" ;;
esac
printf 'Next: vortexify --version, then vortexify login\n'
printf 'Skills: vortexify skills install --agent codex (or --agent claude)\n'
