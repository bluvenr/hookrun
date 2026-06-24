#!/bin/sh
# HookRun Installer
# Usage: curl -fsSL https://bluvenr.github.io/hookrun/install.sh | bash
#
# Environment variables:
#   HOOKRUN_VERSION  - Install a specific version (e.g. "1.1.3"). Default: latest
#   HOOKRUN_INSTALL_DIR - Binary install path. Default: /usr/local/bin
#   HOOKRUN_INIT_DIR  - Directory for init config. Default: current directory
#   HOOKRUN_TEMPLATE  - Init template: generic, github, gitlab. Default: generic

set -e

# ─── Colors ───
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}[INFO]${NC}  %s\n" "$1"; }
ok()    { printf "${GREEN}[OK]${NC}    %s\n" "$1"; }
warn()  { printf "${YELLOW}[WARN]${NC}  %s\n" "$1"; }
fail()  { printf "${RED}[ERROR]${NC} %s\n" "$1"; exit 1; }

REPO="bluvenr/hookrun"
INSTALL_DIR="${HOOKRUN_INSTALL_DIR:-/usr/local/bin}"
INIT_DIR="${HOOKRUN_INIT_DIR:-.}"
TEMPLATE="${HOOKRUN_TEMPLATE:-generic}"

# ─── Pre-flight checks ───
check_command() {
    command -v "$1" >/dev/null 2>&1 || fail "Required command not found: $1. Please install it first."
}

detect_os() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$OS" in
        linux)  OS="linux" ;;
        darwin) OS="darwin" ;;
        *)      fail "Unsupported OS: $OS. HookRun install script supports Linux and macOS." ;;
    esac
}

detect_arch() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64)   ARCH="amd64" ;;
        aarch64|arm64)   ARCH="arm64" ;;
        *)               fail "Unsupported architecture: $ARCH. Supported: amd64, arm64." ;;
    esac
}

detect_http_client() {
    if command -v curl >/dev/null 2>&1; then
        HTTP_CLIENT="curl"
    elif command -v wget >/dev/null 2>&1; then
        HTTP_CLIENT="wget"
    else
        fail "Neither curl nor wget found. Please install one of them first."
    fi
}

http_get() {
    if [ "$HTTP_CLIENT" = "curl" ]; then
        curl -fsSL "$1"
    else
        wget -qO- "$1"
    fi
}

http_download() {
    if [ "$HTTP_CLIENT" = "curl" ]; then
        curl -fsSL -o "$2" "$1"
    else
        wget -qO "$2" "$1"
    fi
}

# ─── Resolve version ───
resolve_version() {
    if [ -n "$HOOKRUN_VERSION" ]; then
        VERSION="$HOOKRUN_VERSION"
        # Strip leading 'v' if present
        VERSION="${VERSION#v}"
        info "Using specified version: v${VERSION}"
    else
        info "Fetching latest release version..."
        LATEST_URL="https://api.github.com/repos/${REPO}/releases/latest"
        TAG=$(http_get "$LATEST_URL" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/')
        if [ -z "$TAG" ]; then
            fail "Failed to fetch latest version from GitHub API. Try setting HOOKRUN_VERSION manually."
        fi
        VERSION="${TAG#v}"
        ok "Latest version: v${VERSION}"
    fi
}

# ─── Download & Install ───
download_and_install() {
    ASSET="hookrun-v${VERSION}-${OS}-${ARCH}.tar.gz"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/v${VERSION}/${ASSET}"
    TMP_DIR=$(mktemp -d)
    TMP_FILE="${TMP_DIR}/${ASSET}"

    info "Downloading ${ASSET}..."
    http_download "$DOWNLOAD_URL" "$TMP_FILE" || fail "Download failed: ${DOWNLOAD_URL}"

    info "Extracting..."
    tar -xzf "$TMP_FILE" -C "$TMP_DIR"

    BINARY_NAME="hookrun-${OS}-${ARCH}"
    if [ ! -f "${TMP_DIR}/${BINARY_NAME}" ]; then
        fail "Binary not found in archive: ${BINARY_NAME}"
    fi

    # Check write permission
    if [ ! -w "$INSTALL_DIR" ]; then
        warn "No write permission to ${INSTALL_DIR}, trying with sudo..."
        sudo mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/hookrun"
        sudo chmod +x "${INSTALL_DIR}/hookrun"
    else
        mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/hookrun"
        chmod +x "${INSTALL_DIR}/hookrun"
    fi

    # Cleanup
    rm -rf "$TMP_DIR"

    ok "Installed to ${INSTALL_DIR}/hookrun"
}

# ─── Verify installation ───
verify_install() {
    if command -v hookrun >/dev/null 2>&1; then
        INSTALLED_VER=$("${INSTALL_DIR}/hookrun" version 2>/dev/null | head -1 || echo "unknown")
        ok "Verification passed: ${INSTALLED_VER}"
    else
        warn "hookrun not found in PATH. You may need to add ${INSTALL_DIR} to your PATH."
        warn "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi
}

# ─── Init config (optional) ───
init_config() {
    if [ "$INIT_DIR" = "skip" ]; then
        return
    fi

    HOOKRUN_BIN="${INSTALL_DIR}/hookrun"
    if [ ! -x "$HOOKRUN_BIN" ]; then
        HOOKRUN_BIN="hookrun"
    fi

    # Check if config already exists
    if [ -f "${INIT_DIR}/config.yaml" ]; then
        warn "config.yaml already exists in ${INIT_DIR}, skipping init."
        return
    fi

    info "Initializing configuration in ${INIT_DIR}..."
    mkdir -p "${INIT_DIR}"

    # Run init in target directory
    (cd "${INIT_DIR}" && "$HOOKRUN_BIN" init --template "$TEMPLATE" --force)

    ok "Configuration initialized in ${INIT_DIR}"
    echo ""
    info "Edit ${INIT_DIR}/hooks/example.yaml to define your webhook rules"
}

# ─── Print summary ───
print_summary() {
    echo ""
    printf "${BOLD}HookRun v${VERSION} installed successfully!${NC}\n"
    echo ""
    echo "  Quick commands:"
    echo "    hookrun validate          # Check configuration"
    echo "    hookrun start             # Start server (daemon mode)"
    echo "    hookrun start -f          # Start in foreground"
    echo "    hookrun status            # Check server status"
    echo "    curl localhost:9000/health  # Health check"
    echo ""
    echo "  Docs: https://bluvenr.github.io/hookrun/"
    echo "  Repo: https://github.com/${REPO}"
    echo ""
}

# ─── Main ───
main() {
    echo ""
    printf "${BOLD}  HookRun Installer${NC}\n"
    echo "  ─────────────────"
    echo ""

    check_command tar
    detect_http_client
    detect_os
    detect_arch
    info "Platform: ${OS}/${ARCH}"

    resolve_version
    download_and_install
    verify_install
    init_config
    print_summary
}

main
