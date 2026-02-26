#!/usr/bin/env bash
# install.sh — install the latest release of mint
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/SpiceLabsHQ/Mint/main/install.sh | sh
#
# Installs to /usr/local/bin by default. Set MINT_INSTALL_DIR to override.
# After the initial install, use `mint update` to upgrade.

set -euo pipefail

REPO="SpiceLabsHQ/Mint"
BINARY="mint"
INSTALL_DIR="${MINT_INSTALL_DIR:-/usr/local/bin}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
ok()    { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
fatal() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || fatal "required tool not found: $1"
}

# ---------------------------------------------------------------------------
# Platform detection
# ---------------------------------------------------------------------------

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux)  echo "linux"  ;;
    *)      fatal "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    arm64|aarch64)  echo "arm64" ;;
    *)              fatal "unsupported architecture: $(uname -m)" ;;
  esac
}

# ---------------------------------------------------------------------------
# Fetch latest release tag from GitHub API
# ---------------------------------------------------------------------------

latest_version() {
  local tag
  tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  [ -n "$tag" ] || fatal "could not determine latest release"
  echo "$tag"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

need curl
need tar
need sha256sum 2>/dev/null || need shasum

OS=$(detect_os)
ARCH=$(detect_arch)

info "Fetching latest release..."
VERSION=$(latest_version)
ok "Latest version: ${VERSION}"

# Strip leading 'v' for the asset filename (GoReleaser omits it in the name).
VERSION_NUM="${VERSION#v}"
ASSET="mint_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

info "Downloading ${ASSET}..."
curl -fsSL "${BASE_URL}/${ASSET}" -o "${TMPDIR}/${ASSET}"
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMPDIR}/checksums.txt"

info "Verifying checksum..."
cd "$TMPDIR"
if command -v sha256sum >/dev/null 2>&1; then
  grep "${ASSET}" checksums.txt | sha256sum --check --status \
    || fatal "checksum verification failed"
else
  # macOS ships shasum, not sha256sum
  grep "${ASSET}" checksums.txt | sed 's/  / /' | shasum -a 256 --check --status \
    || fatal "checksum verification failed"
fi
ok "Checksum verified"
cd - >/dev/null

info "Extracting..."
tar -xzf "${TMPDIR}/${ASSET}" -C "${TMPDIR}" "${BINARY}"

info "Installing to ${INSTALL_DIR}..."
if [ ! -w "${INSTALL_DIR}" ]; then
  sudo install -m 755 "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  install -m 755 "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

ok "mint ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"
echo
echo "Run 'mint --help' to get started."
echo "Run 'mint update' to upgrade to future releases."
