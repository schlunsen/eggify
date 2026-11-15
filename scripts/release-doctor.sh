#!/usr/bin/env bash
# Checks that this machine has everything needed to build and release Eggify:
# toolchains, signing identities, the notary profile, the App Store Connect
# key and SSH access to the web server. Read-only; prints ✓ / ✗ per item.
#   just release-doctor
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "$ROOT/scripts/release-env.sh"

APPLE_TEAM_ID="${APPLE_TEAM_ID:-}"
NOTARY_PROFILE="${NOTARY_PROFILE:-}"
DEPLOY_HOST="${DEPLOY_HOST:-}"
fail=0
ok()  { printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad() { printf '  \033[31m✗\033[0m %s\n' "$1"; fail=1; }
check() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else bad "$1${3:+ — $3}"; fi; }

echo "Toolchains"
for t in go node npm just xcodebuild rsync jq python3; do check "$t" "command -v $t" "install $t"; done
check "Go ≥ 1.26" "go version | grep -Eq 'go1\.(2[6-9]|[3-9][0-9])'"
check "python3 PyJWT (TestFlight status check)" "python3 -c 'import jwt'" "pip3 install pyjwt cryptography"

echo "Release settings (env or ~/.config/eggify/release.env)"
for v in APPLE_TEAM_ID DEPLOY_HOST DEPLOY_DIR PUBLIC_BASE_URL; do
  check "$v set" "[[ -n '${!v:-}' ]]" "add it to ~/.config/eggify/release.env"
done
for v in EGGIFY_BUILD_RELAY_ADDR EGGIFY_BUILD_UPDATE_MANIFEST_URL EGGIFY_BUILD_DOWNLOAD_URL EGGIFY_BUILD_RELAY_ATS_DOMAIN; do
  if [[ -z "${!v:-}" ]]; then printf '  \033[33m!\033[0m %s not set: that feature is off in the build\n' "$v"; else ok "$v set"; fi
done

echo "Desktop app (Developer ID + notarization)"
check "Developer ID Application identity for $APPLE_TEAM_ID" "security find-identity -v -p codesigning | grep -q 'Developer ID Application.*($APPLE_TEAM_ID)'"
# Notarization works with the keychain profile or, as a fallback, the App
# Store Connect API key (desktop-release.sh notary_auth).
NOTARY_KEY="${ASC_KEY_PATH:-$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID:-x}.p8}"
check "notarytool credentials (NOTARY_PROFILE '${NOTARY_PROFILE:-unset}' or App Store Connect key)" \
  "xcrun notarytool history --keychain-profile '$NOTARY_PROFILE' || xcrun notarytool history --key '$NOTARY_KEY' --key-id '${ASC_KEY_ID:-}' --issuer '${ASC_ISSUER_ID:-}'" \
  "xcrun notarytool store-credentials <profile> --apple-id … --team-id $APPLE_TEAM_ID, or set ASC_KEY_ID/ASC_ISSUER_ID/ASC_KEY_PATH"
check "SSH to $DEPLOY_HOST" "ssh -o BatchMode=yes -o ConnectTimeout=8 '$DEPLOY_HOST' true"

echo "iOS / TestFlight"
check "ASC_KEY_ID set" "[[ -n '${ASC_KEY_ID:-}' ]]" "add it to ~/.config/eggify/release.env"
check "ASC_ISSUER_ID set" "[[ -n '${ASC_ISSUER_ID:-}' ]]" "add it to ~/.config/eggify/release.env"
check "App Store Connect key file" "[[ -f '${ASC_KEY_PATH:-$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID:-x}.p8}' ]]"
check "Apple Development/Distribution identity for $APPLE_TEAM_ID" "security find-identity -v -p codesigning | grep -Eq '(Apple Distribution|Apple Development).*'"

echo "Project"
check "frontend dependencies (internal/server/frontend/node_modules)" "[[ -d '$ROOT/internal/server/frontend/node_modules' ]]" "cd internal/server/frontend && npm ci"
check "frontend build (embedded by the Go server)" "[[ -f '$ROOT/internal/server/frontend/.output/public/index.html' ]]" "cd internal/server/frontend && npm run generate"
check "landing dependencies" "[[ -d '$ROOT/landing/node_modules' ]]" "cd landing && npm ci"

echo
if [[ $fail == 0 ]]; then echo "All set: just desktop-release · just ios-testflight · landing/deploy.sh"; else echo "Fix the ✗ items above."; fi
exit $fail
