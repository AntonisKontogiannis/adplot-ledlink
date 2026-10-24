#!/usr/bin/env bash
# Build the LED Link desktop app and its installers (.deb, AppImage, tar.gz, Windows zip).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "==> building Go gateway binaries"
export CGO_ENABLED=0
mkdir -p dist
go build -trimpath -ldflags '-s -w' -o dist/ledlink-linux-amd64 ./cmd/ledlink
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w' \
  -o dist/ledlink-windows-amd64.exe ./cmd/ledlink

echo "==> generating app icons"
python3 scripts/make-icon.py

cd desktop
if [ ! -d node_modules ]; then
  echo "==> installing desktop dependencies"
  npm install --no-audit --no-fund
fi

echo "==> packaging Linux (deb, AppImage, tar.gz)"
npx electron-builder --linux deb AppImage tar.gz

echo "==> packaging Windows (portable zip)"
npx electron-builder --win zip

echo
echo "artifacts in desktop/release:"
ls -lh release/*.deb release/*.AppImage release/*.tar.gz release/*.zip 2>/dev/null
