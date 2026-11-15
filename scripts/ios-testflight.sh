#!/usr/bin/env bash
# Build the iOS app (ios/eggify) and upload it to TestFlight.
#
# Archives with automatic signing (team APPLE_TEAM_ID) through an App Store
# Connect API key, so no Apple ID has to be signed in to Xcode, exports for
# App Store Connect with destination "upload", then polls App Store Connect
# until the build shows up.
#
# Environment:
#   ASC_KEY_ID         App Store Connect API key id (required)
#   ASC_ISSUER_ID      API key issuer id (required; App Store Connect >
#                      Users and Access > Integrations > App Store Connect API)
#   ASC_KEY_PATH       .p8 key file (default:
#                      ~/.appstoreconnect/private_keys/AuthKey_$ASC_KEY_ID.p8)
#   BUILD_NUMBER       CFBundleVersion (default: local timestamp YYYYMMDDHHMM,
#                      always above earlier uploads)
#   MARKETING_VERSION  CFBundleShortVersionString (default: package.json version)
#   WORK_DIR           where the archive and export go (default: a temp dir,
#                      deleted afterwards)
#   WAIT_FOR_BUILD     0 to skip polling App Store Connect (default: 1;
#                      needs python3 with PyJWT + cryptography)
#
# Usage:  ASC_KEY_ID=... ASC_ISSUER_ID=... ./scripts/ios-testflight.sh
#    or:  just ios-testflight
#
# Settings can live in ~/.config/eggify/release.env (see scripts/release-env.sh).
# DRY_RUN=1 archives and exports a signed .ipa locally but does not upload.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=release-env.sh
source "$ROOT/scripts/release-env.sh"
PROJECT="$ROOT/ios/eggify/eggify.xcodeproj"
SCHEME="eggify"
BUNDLE_ID="com.schlunsen.eggify"
: "${APPLE_TEAM_ID:?Set APPLE_TEAM_ID (Apple developer team id)}"
TEAM_ID="$APPLE_TEAM_ID"

: "${ASC_KEY_ID:?Set ASC_KEY_ID (App Store Connect API key id)}"
: "${ASC_ISSUER_ID:?Set ASC_ISSUER_ID (App Store Connect API issuer id)}"
ASC_KEY_PATH="${ASC_KEY_PATH:-$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8}"
[[ -f "$ASC_KEY_PATH" ]] || { echo "API key not found: $ASC_KEY_PATH" >&2; exit 1; }
export ASC_KEY_ID ASC_ISSUER_ID ASC_KEY_PATH

# The relay's wildcard domain needs an ATS exception: a local copy of Info.plist
# written by scripts/ios-relay-config.sh (Xcode does not expand build settings in
# plist keys). Without one the app cannot reach a relay address.
# ALLOW_NO_RELAY_ATS=1 opts out.
if [[ -n "${EGGIFY_BUILD_RELAY_ATS_DOMAIN:-}" ]]; then
  "$ROOT/scripts/ios-relay-config.sh" "$EGGIFY_BUILD_RELAY_ATS_DOMAIN"
elif [[ ! -f "$ROOT/ios/eggify/Info.local.plist" || ! -f "$ROOT/ios/eggify/Local.xcconfig" ]]; then
  msg="no relay ATS domain: set EGGIFY_BUILD_RELAY_ATS_DOMAIN or run scripts/ios-relay-config.sh <domain>"
  if [[ "${DRY_RUN:-0}" == 1 || "${ALLOW_NO_RELAY_ATS:-0}" == 1 ]]; then echo "WARNING: $msg" >&2
  else echo "ERROR: $msg (ALLOW_NO_RELAY_ATS=1 to upload anyway)" >&2; exit 1; fi
fi

BUILD_NUMBER="${BUILD_NUMBER:-$(date +%Y%m%d%H%M)}"
if [[ -z "${MARKETING_VERSION:-}" ]]; then
  MARKETING_VERSION="$(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)".*/\1/p' "$ROOT/package.json" | head -1)"
fi
[[ -n "$MARKETING_VERSION" ]] || { echo "Could not determine MARKETING_VERSION" >&2; exit 1; }

CLEANUP=0
if [[ -z "${WORK_DIR:-}" ]]; then
  WORK_DIR="$(mktemp -d -t eggify-testflight)"
  CLEANUP=1
fi
mkdir -p "$WORK_DIR"
cleanup() { if [[ "$CLEANUP" == 1 ]]; then rm -rf "$WORK_DIR"; fi; }
trap cleanup EXIT

ARCHIVE="$WORK_DIR/eggify.xcarchive"
AUTH=(-allowProvisioningUpdates
      -authenticationKeyPath "$ASC_KEY_PATH"
      -authenticationKeyID "$ASC_KEY_ID"
      -authenticationKeyIssuerID "$ASC_ISSUER_ID")

echo "==> Archiving Eggify $MARKETING_VERSION ($BUILD_NUMBER)"
xcodebuild archive \
  -project "$PROJECT" -scheme "$SCHEME" -configuration Release \
  -destination 'generic/platform=iOS' \
  -archivePath "$ARCHIVE" \
  -derivedDataPath "$WORK_DIR/DerivedData" \
  DEVELOPMENT_TEAM="$TEAM_ID" CODE_SIGN_STYLE=Automatic \
  CURRENT_PROJECT_VERSION="$BUILD_NUMBER" MARKETING_VERSION="$MARKETING_VERSION" \
  "${AUTH[@]}" -quiet

cat > "$WORK_DIR/ExportOptions.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key><string>app-store-connect</string>
  <key>destination</key><string>$([[ "${DRY_RUN:-0}" == 1 ]] && echo export || echo upload)</string>
  <key>teamID</key><string>$TEAM_ID</string>
  <key>signingStyle</key><string>automatic</string>
  <key>uploadSymbols</key><true/>
  <key>manageAppVersionAndBuildNumber</key><false/>
</dict>
</plist>
PLIST

if [[ "${DRY_RUN:-0}" == 1 ]]; then echo "==> Exporting (dry run, no upload)"; else echo "==> Uploading to App Store Connect"; fi
xcodebuild -exportArchive \
  -archivePath "$ARCHIVE" \
  -exportOptionsPlist "$WORK_DIR/ExportOptions.plist" \
  -exportPath "$WORK_DIR/export" \
  "${AUTH[@]}" -quiet

if [[ "${DRY_RUN:-0}" == 1 ]]; then
  IPA="$(ls "$WORK_DIR"/export/*.ipa 2>/dev/null | head -1)"
  echo "==> Dry run OK: signed $MARKETING_VERSION ($BUILD_NUMBER) at ${IPA:-$WORK_DIR/export} (not uploaded)"
  exit 0
fi
echo "==> Uploaded $MARKETING_VERSION ($BUILD_NUMBER)"

if [[ "${WAIT_FOR_BUILD:-1}" == 1 ]]; then
  echo "==> Waiting for App Store Connect to list the build"
  BUNDLE_ID="$BUNDLE_ID" BUILD_NUMBER="$BUILD_NUMBER" python3 - <<'PY'
import json, os, sys, time, urllib.parse, urllib.request
try:
    import jwt
except ImportError:
    sys.exit("PyJWT not installed (pip3 install pyjwt cryptography); skipping the status check")

key = open(os.environ["ASC_KEY_PATH"]).read()

def get(path):
    now = int(time.time())
    token = jwt.encode({"iss": os.environ["ASC_ISSUER_ID"], "iat": now, "exp": now + 600,
                        "aud": "appstoreconnect-v1"}, key, algorithm="ES256",
                       headers={"kid": os.environ["ASC_KEY_ID"]})
    req = urllib.request.Request("https://api.appstoreconnect.apple.com" + path,
                                 headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req) as r:
        return json.load(r)

bundle = urllib.parse.quote(os.environ["BUNDLE_ID"])
apps = get(f"/v1/apps?filter[bundleId]={bundle}")["data"]
if not apps:
    sys.exit(f"No App Store Connect app for {os.environ['BUNDLE_ID']}")
app = apps[0]["id"]
build = os.environ["BUILD_NUMBER"]
state = None
for _ in range(60):  # up to ~30 min
    data = get(f"/v1/builds?filter[app]={app}&filter[version]={build}")["data"]
    if data:
        new = data[0]["attributes"]["processingState"]
        if new != state:
            state = new
            print(f"build {build}: {state}", flush=True)
        if state in ("VALID", "FAILED", "INVALID"):
            break
    time.sleep(30)
if state is None:
    sys.exit(f"build {build} not listed yet; check App Store Connect > TestFlight")
if state != "VALID":
    sys.exit(f"build {build} ended as {state}")
PY
fi
