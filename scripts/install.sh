#!/usr/bin/env sh
# agent-netx installer for macOS / Linux
# Usage:  curl -fsSL https://github.com/yejinlei/agent-netx/releases/latest/download/install.sh | sh

set -eu

REPO="yejinlei/agent-netx"
API="https://api.github.com/repos/$REPO/releases/latest"
BASE="https://github.com/$REPO/releases/latest/download"

GOOS="$(uname -s | tr '[:upper:]' '[:lower:]')"
GOARCH="$(uname -m)"
case "$GOARCH" in
    x86_64|amd64) GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *) echo "unsupported arch: $GOARCH"; exit 1 ;;
esac

TAG="$(curl -fsS "$API" | grep '"tag_name":' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
ASSET="agent-netx-$GOOS-$GOARCH"
[ "$GOOS" = "windows" ] && ASSET="${ASSET}.exe"

URL="${BASE}/${ASSET}"
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "$INSTALL_DIR"
DEST="$INSTALL_DIR/agent-netx"

echo "agent-netx $TAG — downloading $ASSET ..."
TMP="$(mktemp)"
curl -fsSL "$URL" -o "$TMP"
chmod +x "$TMP"
mv "$TMP" "$DEST"

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) echo "export PATH=\"$INSTALL_DIR:\$PATH\"" >> "$HOME/.profile";;
esac

echo "  installed to: $DEST"
echo "  run: $DEST --version"
