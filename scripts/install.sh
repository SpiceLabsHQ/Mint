#!/bin/sh
# Mint install script
#
# Downloads the latest mint binary from GitHub Releases, verifies its SHA256
# checksum, and installs it to /usr/local/bin (or ~/.local/bin as fallback).
#
# Usage (pipe install):
#   curl -fsSL https://raw.githubusercontent.com/SpiceLabsHQ/Mint/main/scripts/install.sh | sh
#
# Usage (local):
#   sh scripts/install.sh
#
# Environment overrides (for testing / CI):
#   MINT_VERSION   — install a specific version (e.g. "v1.2.3"); default: latest
#   MINT_INSTALL_DIR — override install directory
#
# This script follows ADR-0015: it places only the binary and writes no config
# files. It follows ADR-0020: SHA256 checksum verification only (no signing).

set -eu

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

GITHUB_ORG="SpiceLabsHQ"
GITHUB_REPO="Mint"
BINARY_NAME="mint"
RELEASES_API="https://api.github.com/repos/${GITHUB_ORG}/${GITHUB_REPO}/releases/latest"

# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------

# Print to stderr so it doesn't interfere with piped output.
log()  { printf '%s\n'   "$*" >&2; }
info() { printf '  %s\n' "$*" >&2; }
die()  { log "error: $*"; exit 1; }

# ---------------------------------------------------------------------------
# Dependency checks
# ---------------------------------------------------------------------------

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        case "$1" in
            curl)  die "curl is required but not found. Install it with: apt-get install curl  OR  brew install curl" ;;
            tar)   die "tar is required but not found. Install it with: apt-get install tar" ;;
            unzip) die "unzip is required but not found. Install it with: apt-get install unzip  OR  brew install unzip" ;;
            sha256sum|shasum)
                die "sha256sum (or shasum) is required but not found. Install coreutils: apt-get install coreutils  OR  brew install coreutils" ;;
            *)     die "$1 is required but not found." ;;
        esac
    fi
}

# ---------------------------------------------------------------------------
# OS / arch detection
# ---------------------------------------------------------------------------

detect_os() {
    _raw="$(uname -s)"
    case "${_raw}" in
        Linux)  printf 'linux' ;;
        Darwin) printf 'darwin' ;;
        *) die "unsupported operating system: ${_raw}. Mint supports linux and darwin." ;;
    esac
}

detect_arch() {
    _raw="$(uname -m)"
    case "${_raw}" in
        x86_64|amd64) printf 'amd64' ;;
        aarch64|arm64) printf 'arm64' ;;
        *) die "unsupported architecture: ${_raw}. Mint supports amd64 and arm64." ;;
    esac
}

# ---------------------------------------------------------------------------
# Archive format (GoReleaser convention: darwin → zip, linux → tar.gz)
# ---------------------------------------------------------------------------

archive_ext() {
    _os="$1"
    case "${_os}" in
        darwin) printf 'tar.gz' ;;
        *)      printf 'tar.gz' ;;
    esac
}

# ---------------------------------------------------------------------------
# SHA256 computation
# ---------------------------------------------------------------------------

sha256_file() {
    # Normalise across Linux (sha256sum) and macOS (shasum -a 256).
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        die "no SHA256 utility found. Install coreutils: apt-get install coreutils  OR  brew install coreutils"
    fi
}

# ---------------------------------------------------------------------------
# Version resolution
# ---------------------------------------------------------------------------

resolve_version() {
    # If MINT_VERSION is already set, use it directly.
    if [ -n "${MINT_VERSION:-}" ]; then
        printf '%s' "${MINT_VERSION}"
        return
    fi

    log "Fetching latest release version..."
    _response="$(curl -fsSL \
        -H 'Accept: application/vnd.github+json' \
        "${RELEASES_API}")" \
        || die "failed to fetch latest release from GitHub. Check your network connection."

    # Extract tag_name with portable sed (no jq dependency).
    _version="$(printf '%s' "${_response}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"

    if [ -z "${_version}" ]; then
        die "could not determine latest version from GitHub Releases API response."
    fi

    printf '%s' "${_version}"
}

# ---------------------------------------------------------------------------
# Download + verify + extract + install
# ---------------------------------------------------------------------------

main() {
    # Enforce HTTPS — MINT_VERSION and MINT_INSTALL_DIR may be set in env.
    require_cmd curl

    OS="$(detect_os)"
    ARCH="$(detect_arch)"
    EXT="$(archive_ext "${OS}")"
    VERSION="$(resolve_version)"

    # Strip leading 'v' for the archive filename (GoReleaser convention).
    VERSION_BARE="$(printf '%s' "${VERSION}" | sed 's/^v//')"

    ARCHIVE_NAME="${BINARY_NAME}_${VERSION_BARE}_${OS}_${ARCH}.${EXT}"
    CHECKSUMS_NAME="checksums.txt"

    BASE_URL="https://github.com/${GITHUB_ORG}/${GITHUB_REPO}/releases/download/${VERSION}"
    ARCHIVE_URL="${BASE_URL}/${ARCHIVE_NAME}"
    CHECKSUMS_URL="${BASE_URL}/${CHECKSUMS_NAME}"

    # Enforce HTTPS on all constructed URLs (defence in depth).
    case "${ARCHIVE_URL}" in
        https://*) ;;
        *) die "refusing to download from non-HTTPS URL: ${ARCHIVE_URL}" ;;
    esac
    case "${CHECKSUMS_URL}" in
        https://*) ;;
        *) die "refusing to download checksums from non-HTTPS URL: ${CHECKSUMS_URL}" ;;
    esac

    log "Installing mint ${VERSION} (${OS}/${ARCH})"

    # Create a private temp directory that is cleaned up on exit.
    WORKDIR="$(mktemp -d)"
    # shellcheck disable=SC2064
    trap "rm -rf '${WORKDIR}'" EXIT INT TERM

    ARCHIVE_PATH="${WORKDIR}/${ARCHIVE_NAME}"
    CHECKSUMS_PATH="${WORKDIR}/${CHECKSUMS_NAME}"

    # --- Download ---
    info "Downloading ${ARCHIVE_NAME}..."
    curl -fsSL --output "${ARCHIVE_PATH}" "${ARCHIVE_URL}" \
        || die "download failed: ${ARCHIVE_URL}
  Check your network connection and that version ${VERSION} exists."

    info "Downloading ${CHECKSUMS_NAME}..."
    curl -fsSL --output "${CHECKSUMS_PATH}" "${CHECKSUMS_URL}" \
        || die "checksum download failed: ${CHECKSUMS_URL}"

    # --- Verify checksum ---
    info "Verifying SHA256 checksum..."

    ACTUAL_HASH="$(sha256_file "${ARCHIVE_PATH}")"

    # Find expected hash for our archive in checksums.txt.
    # Format: "<hash>  <filename>" (two spaces, sha256sum convention).
    EXPECTED_HASH="$(grep "[[:space:]]${ARCHIVE_NAME}$" "${CHECKSUMS_PATH}" | awk '{print $1}')"

    if [ -z "${EXPECTED_HASH}" ]; then
        die "no checksum entry found for ${ARCHIVE_NAME} in checksums.txt.
  The release may be incomplete or the archive name does not match the expected pattern."
    fi

    if [ "${ACTUAL_HASH}" != "${EXPECTED_HASH}" ]; then
        die "SHA256 checksum mismatch — the download may be corrupted or tampered with.
  Expected: ${EXPECTED_HASH}
  Actual:   ${ACTUAL_HASH}
  Do NOT use this binary. Re-run the installer to try again."
    fi

    info "Checksum verified."

    # --- Extract binary ---
    info "Extracting ${BINARY_NAME}..."

    case "${EXT}" in
        tar.gz)
            require_cmd tar
            tar -xzf "${ARCHIVE_PATH}" -C "${WORKDIR}" "${BINARY_NAME}" 2>/dev/null \
                || tar -xzf "${ARCHIVE_PATH}" -C "${WORKDIR}" \
                || die "failed to extract archive: ${ARCHIVE_PATH}"
            ;;
        zip)
            require_cmd unzip
            unzip -q "${ARCHIVE_PATH}" -d "${WORKDIR}" \
                || die "failed to extract archive: ${ARCHIVE_PATH}"
            ;;
        *)
            die "unexpected archive format: ${EXT}"
            ;;
    esac

    BINARY_PATH="${WORKDIR}/${BINARY_NAME}"

    if [ ! -f "${BINARY_PATH}" ]; then
        die "extracted binary not found at ${BINARY_PATH}. Archive contents may have changed."
    fi

    chmod 755 "${BINARY_PATH}"

    # --- Install ---
    if [ -n "${MINT_INSTALL_DIR:-}" ]; then
        INSTALL_DIR="${MINT_INSTALL_DIR}"
    elif [ -w "/usr/local/bin" ]; then
        INSTALL_DIR="/usr/local/bin"
    else
        INSTALL_DIR="${HOME}/.local/bin"
        # Ensure the directory exists.
        mkdir -p "${INSTALL_DIR}"
    fi

    INSTALL_PATH="${INSTALL_DIR}/${BINARY_NAME}"

    info "Installing to ${INSTALL_PATH}..."
    mv "${BINARY_PATH}" "${INSTALL_PATH}" \
        || die "failed to install binary to ${INSTALL_PATH}.
  Try running with sudo:  curl -fsSL <url> | sudo sh
  Or set MINT_INSTALL_DIR to a writable directory."

    chmod 755 "${INSTALL_PATH}"

    # --- Done ---
    log ""
    log "mint ${VERSION} installed successfully to ${INSTALL_PATH}"

    # Warn if install dir is not in PATH.
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            log ""
            log "NOTE: ${INSTALL_DIR} is not in your PATH."
            log "Add it to your shell profile:"
            log "  export PATH=\"\$PATH:${INSTALL_DIR}\""
            ;;
    esac

    log ""
    log "Run 'mint --help' to get started."
}

main "$@"
