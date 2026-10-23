#!/usr/bin/env bash
set -euo pipefail

REPO="jherreros/shoulders"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

if [[ "$OS" != "darwin" && "$OS" != "linux" ]]; then
  echo "Unsupported OS: $OS"
  exit 1
fi

# Follow the release redirect rather than using the unauthenticated GitHub
# API, which is limited to 60 requests/hour per IP and fails on shared networks.
VERSION=$(curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" | sed -n 's#.*/releases/tag/##p')
if [[ "$VERSION" != v* ]]; then
  echo "Failed to fetch latest version"
  exit 1
fi

# Remove 'v' prefix for the binary filename
BINARY_VERSION=${VERSION#v}
TARBALL="shoulders_${BINARY_VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

TMP_DIR=$(mktemp -d)
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

ARCHIVE="$TMP_DIR/$TARBALL"
CHECKSUMS="$TMP_DIR/checksums.txt"

echo "Downloading $URL..."
if ! curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "$URL" -o "$ARCHIVE"; then
  echo "Failed to download archive"
  exit 1
fi

if ! curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "$CHECKSUMS_URL" -o "$CHECKSUMS"; then
  echo "Failed to download checksums"
  exit 1
fi

EXPECTED=$(awk -v file="$TARBALL" '$2 == file { print $1; exit }' "$CHECKSUMS")
if [[ -z "$EXPECTED" ]]; then
  echo "Checksum for $TARBALL not found"
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$ARCHIVE" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
fi

if [[ "$ACTUAL" != "$EXPECTED" ]]; then
  echo "Checksum mismatch for $TARBALL"
  exit 1
fi

if ! tar -xzf "$ARCHIVE" -C "$TMP_DIR"; then
  echo "Failed to extract archive"
  exit 1
fi

if [[ ! -f "$TMP_DIR/shoulders" ]]; then
  echo "Binary not found in archive"
  exit 1
fi

if [[ ! -w "$INSTALL_DIR" ]]; then
  echo "Installing to $INSTALL_DIR requires sudo"
  sudo mv "$TMP_DIR/shoulders" "$INSTALL_DIR/shoulders"
  sudo chmod 0755 "$INSTALL_DIR/shoulders"
else
  mv "$TMP_DIR/shoulders" "$INSTALL_DIR/shoulders"
  chmod +x "$INSTALL_DIR/shoulders"
fi

echo "Installed shoulders $VERSION to $INSTALL_DIR/shoulders"
"$INSTALL_DIR/shoulders" --version
