#!/usr/bin/env bash
# Build a macOS .dmg containing node-stats.app (launches the server + opens the
# dashboard in the browser). Run on macOS (needs codesign + hdiutil).
# Usage: make-macos-dmg.sh <version> <path-to-darwin-binary> <output.dmg>
set -euo pipefail
VERSION="${1:?version required}"
BIN="${2:?binary path required}"
OUT="${3:?output dmg path required}"

WORK="$(mktemp -d)"
APP="$WORK/node-stats.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/Resources/node-stats"
chmod +x "$APP/Contents/Resources/node-stats"

cat > "$APP/Contents/MacOS/node-stats" <<'SH'
#!/bin/bash
# Run the server from a writable data dir and open the dashboard.
DATA="$HOME/Library/Application Support/node-stats"
mkdir -p "$DATA"; cd "$DATA" || exit 1
RES="$(cd "$(dirname "$0")/../Resources" && pwd)"
( sleep 2; open "http://localhost:8080" ) &
exec "$RES/node-stats"
SH
chmod +x "$APP/Contents/MacOS/node-stats"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>node-stats</string>
  <key>CFBundleDisplayName</key><string>node-stats</string>
  <key>CFBundleIdentifier</key><string>com.lastskywalker.node-stats</string>
  <key>CFBundleVersion</key><string>${VERSION#v}</string>
  <key>CFBundleShortVersionString</key><string>${VERSION#v}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleExecutable</key><string>node-stats</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
</dict></plist>
PLIST

# Ad-hoc sign so it at least runs after a one-time right-click→Open. A seamless
# download experience needs Apple notarization (Developer ID) — wire separately.
codesign --force --deep --sign - "$APP"

STAGE="$WORK/stage"; mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"; ln -s /Applications "$STAGE/Applications"
mkdir -p "$(dirname "$OUT")"
hdiutil create -volname "node-stats" -srcfolder "$STAGE" -ov -format UDZO "$OUT"
rm -rf "$WORK"
echo "built $OUT"
