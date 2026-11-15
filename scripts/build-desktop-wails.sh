#!/usr/bin/env bash
# Build the Wails v3 desktop app (cmd/eggify-desktop) into dist/wails/Eggify.app.
#
#   scripts/build-desktop-wails.sh            # build frontend if missing, then the .app
#   SKIP_FRONTEND=1 scripts/...               # reuse internal/server/frontend/.output
#   FRESH_FRONTEND=1 scripts/...              # always rebuild the frontend (releases)
#   SIGN_IDENTITY="Developer ID Application: ..." scripts/...   # sign with hardened runtime
#   APPLE_TEAM_ID=ABCDE12345                  # team the self-updater requires (releases)
#   EGGIFY_BUILD_RELAY_ADDR, EGGIFY_BUILD_UPDATE_MANIFEST_URL,
#   EGGIFY_BUILD_DOWNLOAD_URL                 # compiled-in service endpoints (see release-env.sh)
#
# Unsigned builds are ad-hoc signed (codesign -s -) so macOS will launch them
# locally. Releases go through `just desktop-app` / `just desktop-release`
# (scripts/desktop-release.sh), which pick the Developer ID identity, notarize
# and publish. See docs/desktop-wails.md.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DESKTOP="$ROOT/cmd/eggify-desktop"
OUT="$ROOT/dist/wails"
APP="$OUT/Eggify.app"
VERSION="$(sed -n 's/.*Version *= *"\(.*\)".*/\1/p' "$ROOT/internal/version/version.go" | head -1)"
VERSION="${VERSION:-0.0.0}"

if [[ "$(uname)" != "Darwin" ]]; then
  echo "This script builds the macOS .app; on Linux run: (cd cmd/eggify-desktop && go build .)" >&2
  exit 1
fi

# Without APPLE_TEAM_ID the updater refuses to install (it has no team to verify against).
APPLE_TEAM_ID="${APPLE_TEAM_ID:-}"
# Service endpoints are not in the source: a build without them has the feature
# off (no relay address or update manifest).
MOD=github.com/schlunsen/eggify
# The testing environment knobs (update manifest override, fake version, diag script) are off in release builds.
RELEASE_BUILD="${RELEASE_BUILD:-true}"
LDFLAGS="-s -w -X main.releaseBuild=$RELEASE_BUILD -X main.expectedTeamID=$APPLE_TEAM_ID"
[[ -n "${EGGIFY_BUILD_RELAY_ADDR:-}" ]] && LDFLAGS+=" -X $MOD/internal/remoteaccess.DefaultRelayAddr=$EGGIFY_BUILD_RELAY_ADDR"
[[ -n "${EGGIFY_BUILD_UPDATE_MANIFEST_URL:-}" ]] && LDFLAGS+=" -X main.defaultManifestURL=$EGGIFY_BUILD_UPDATE_MANIFEST_URL"
[[ -n "${EGGIFY_BUILD_DOWNLOAD_URL:-}" ]] && LDFLAGS+=" -X main.defaultDownloadURL=$EGGIFY_BUILD_DOWNLOAD_URL"

if [[ "${SKIP_FRONTEND:-}" != "1" && ( "${FRESH_FRONTEND:-}" == "1" || ! -f "$ROOT/internal/server/frontend/.output/public/index.html" ) ]]; then
  echo "==> Building frontend"
  (cd "$ROOT/internal/server/frontend" && npm ci && npm run generate)
fi

echo "==> Building eggify-desktop $VERSION"
mkdir -p "$OUT"
(
  cd "$DESKTOP"
  MACOSX_DEPLOYMENT_TARGET=13.4 CGO_ENABLED=1 \
    go build -tags production -trimpath \
      -ldflags="$LDFLAGS" -o "$OUT/eggify-desktop" .
) 2> >(grep -v -E "deprecated|c-api.h|SHERPA|cgo-gcc-prolog|duplicate libraries" >&2)

echo "==> Assembling $APP"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
mv "$OUT/eggify-desktop" "$APP/Contents/MacOS/eggify-desktop"
sed "s/@VERSION@/$VERSION/g" "$DESKTOP/build/darwin/Info.plist" > "$APP/Contents/Info.plist"
cp "$ROOT/src-tauri/icons/icon.icns" "$APP/Contents/Resources/icon.icns"

# sherpa-onnx (speech) is linked against dylibs in the Go module cache via an
# absolute rpath; bundle them like scripts/package-release.sh does.
SHERPA_DIR="$(cd "$ROOT" && go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-macos)"
case "$(uname -m)" in arm64) TRIPLE=aarch64-apple-darwin ;; *) TRIPLE=x86_64-apple-darwin ;; esac
mkdir -p "$APP/Contents/Frameworks"
# Only the libraries the binary links (see otool -L); the module also ships
# unversioned duplicates.
for lib in $(otool -L "$APP/Contents/MacOS/eggify-desktop" | sed -n 's#^[[:space:]]*@rpath/\([^ ]*\.dylib\).*#\1#p'); do
  cp "$SHERPA_DIR/lib/$TRIPLE/$lib" "$APP/Contents/Frameworks/"
done
chmod u+w "$APP/Contents/Frameworks/"*
install_name_tool -rpath "$SHERPA_DIR/lib/$TRIPLE" '@executable_path/../Frameworks' \
  "$APP/Contents/MacOS/eggify-desktop" 2>/dev/null

if [[ -n "${SIGN_IDENTITY:-}" ]]; then
  echo "==> Signing with $SIGN_IDENTITY (hardened runtime)"
  for lib in "$APP/Contents/Frameworks/"*.dylib; do
    codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$lib"
  done
  codesign --force --options runtime --timestamp \
    --entitlements "$DESKTOP/build/darwin/entitlements.plist" \
    --sign "$SIGN_IDENTITY" "$APP"
else
  echo "==> Ad-hoc signing (not notarizable)"
  for lib in "$APP/Contents/Frameworks/"*.dylib; do codesign --force --sign - "$lib"; done
  codesign --force --sign - "$APP"
fi

echo "==> Done: $APP ($(du -sh "$APP" | cut -f1))"
