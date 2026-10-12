#!/bin/sh
# ccswitch installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/web-pruthvi-sigmasolve/ccswitch/main/install.sh | sh
#
# Environment:
#   CCSWITCH_VERSION      Version to install, for example v0.1.0. Default: latest release.
#   CCSWITCH_INSTALL_DIR  Install directory. Default: $HOME/.local/bin
#
# Flags:
#   --system   Install to /usr/local/bin using sudo.
#   --help     Show this help.

set -eu

REPO="web-pruthvi-sigmasolve/ccswitch"
BIN="ccswitch"

say() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

usage() {
  cat <<'EOF'
ccswitch installer (Linux and macOS)

Usage: install.sh [--system] [--help]

  --system   Install to /usr/local/bin using sudo.
  --help     Show this help.

Environment:
  CCSWITCH_VERSION      Version to install, for example v0.1.0 (default: latest)
  CCSWITCH_INSTALL_DIR  Install directory (default: $HOME/.local/bin)

The script downloads the release archive and checksums.txt from GitHub,
verifies the SHA-256 sum, and installs the binary. It changes nothing else.
EOF
}

SYSTEM=0
for arg in "$@"; do
  case "$arg" in
    --help|-h) usage; exit 0 ;;
    --system) SYSTEM=1 ;;
    *) err "unknown option: $arg"; usage >&2; exit 1 ;;
  esac
done

have() { command -v "$1" >/dev/null 2>&1; }

# Pick a downloader.
if have curl; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  fetch_stdout() { curl -fsSL "$1"; }
elif have wget; then
  fetch() { wget -q "$1" -O "$2"; }
  fetch_stdout() { wget -qO- "$1"; }
else
  die "need curl or wget to download ccswitch"
fi

# Detect OS.
case "$(uname -s)" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported OS: $(uname -s). On Windows use install.ps1." ;;
esac

# Detect architecture.
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU architecture: $(uname -m). ccswitch supports amd64 and arm64." ;;
esac

# Resolve version.
VERSION="${CCSWITCH_VERSION:-}"
if [ -z "$VERSION" ]; then
  say "Looking up the latest release..."
  json="$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest")" \
    || die "could not reach the GitHub API. Set CCSWITCH_VERSION (for example v0.1.0) and retry."
  VERSION="$(printf '%s\n' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$VERSION" ] || die "could not read the latest version from GitHub. Set CCSWITCH_VERSION and retry."
fi
case "$VERSION" in
  v*) TAG="$VERSION" ;;
  *)  TAG="v$VERSION" ;;
esac
NUM="${TAG#v}"

ARCHIVE="${BIN}_${NUM}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"

# Choose checksum tool.
if have sha256sum; then
  sha_of() { sha256sum "$1" | awk '{print $1}'; }
elif have shasum; then
  sha_of() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  die "need sha256sum or shasum to verify the download"
fi

have tar || die "need tar to unpack the download"

# Choose install directory.
if [ "$SYSTEM" -eq 1 ]; then
  DEST="/usr/local/bin"
  if [ "$(id -u)" -eq 0 ]; then SUDO=""; else
    have sudo || die "--system needs root or sudo"
    SUDO="sudo"
  fi
else
  DEST="${CCSWITCH_INSTALL_DIR:-$HOME/.local/bin}"
  SUDO=""
  mkdir -p "$DEST" || die "could not create $DEST. Set CCSWITCH_INSTALL_DIR or use --system."
fi

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t ccswitch)"
trap 'rm -rf "$TMP"' EXIT INT TERM

say "Downloading ${ARCHIVE} (${TAG})..."
fetch "${BASE_URL}/${ARCHIVE}" "$TMP/$ARCHIVE" \
  || die "download failed: ${BASE_URL}/${ARCHIVE}. Check the version and your network."
fetch "${BASE_URL}/checksums.txt" "$TMP/checksums.txt" \
  || die "download failed: ${BASE_URL}/checksums.txt"

say "Verifying SHA-256..."
want="$(awk -v f="$ARCHIVE" '$2 == f || $2 == "*" f {print $1; exit}' "$TMP/checksums.txt")"
[ -n "$want" ] || die "$ARCHIVE is not listed in checksums.txt"
got="$(sha_of "$TMP/$ARCHIVE")"
if [ "$want" != "$got" ]; then
  err "checksum mismatch for $ARCHIVE"
  err "expected: $want"
  err "actual:   $got"
  die "refusing to install. Nothing was changed."
fi

say "Extracting..."
tar -xzf "$TMP/$ARCHIVE" -C "$TMP" "$BIN" || die "could not extract $BIN from $ARCHIVE"
[ -f "$TMP/$BIN" ] || die "$BIN not found in the archive"
chmod 0755 "$TMP/$BIN"

say "Installing to ${DEST}/${BIN}..."
if [ -n "$SUDO" ]; then
  $SUDO install -m 0755 "$TMP/$BIN" "$DEST/$BIN" || die "could not install to $DEST"
else
  install -m 0755 "$TMP/$BIN" "$DEST/$BIN" || die "could not install to $DEST"
fi

case ":$PATH:" in
  *":$DEST:"*) ;;
  *)
    say ""
    say "$DEST is not on your PATH. Add it with:"
    say "  export PATH=\"$DEST:\$PATH\""
    say "Put that line in ~/.profile, ~/.bashrc or ~/.zshrc to keep it."
    ;;
esac

say ""
"$DEST/$BIN" version || die "installed, but '$BIN version' failed to run"
say ""
say "Done. Run 'ccswitch' to start, or 'ccswitch doctor' to check your setup."
