#!/usr/bin/env bash
# Signed, notarized, self-updating releases of the Wails desktop app
# (cmd/eggify-desktop), modelled on the n0 desktop release (desktop-build-darwin,
# desktop-dmg, deploy-dmg in n0's Justfile).
#
#   scripts/desktop-release.sh app       # build Eggify.app, signed with Developer ID
#   scripts/desktop-release.sh dmg       # app + styled DMG, sign, notarize, staple,
#                                        # update ZIP, .sha256 files, latest.json
#   scripts/desktop-release.sh gate      # pre-publish signing gate only
#   scripts/desktop-release.sh deploy    # gate, then publish to $PUBLIC_BASE_URL
#   scripts/desktop-release.sh verify    # check the published files over HTTPS
#   scripts/desktop-release.sh rollback <version>   # re-point latest.json + aliases
#
# Environment (defaults in brackets):
#   APPLE_TEAM_ID       (required) team that must sign the app (also compiled
#                       into the self-updater)
#   SIGN_IDENTITY       [auto: first "Developer ID Application" identity of that team]
#   NOTARY_PROFILE      notarytool keychain profile (or use the ASC_* key below)
#                       (xcrun notarytool store-credentials <name> --team-id <team> ...)
#   ASC_KEY_ID, ASC_ISSUER_ID, ASC_KEY_PATH
#                       App Store Connect API key (as for TestFlight): used to
#                       notarize when the keychain profile can't be read, or
#                       always with NOTARY_USE_API_KEY=1
#   DEPLOY_HOST         (deploy/rollback) ssh target, e.g. user@your-host
#   DEPLOY_DIR          (deploy/rollback) download folder on it, e.g. /var/www/example/download
#   PUBLIC_BASE_URL     (dmg/deploy/verify) public URL of that folder, e.g. https://example.com/download
#   RELEASE_PAGE_URL    [<PUBLIC_BASE_URL minus /download>/#download] page the update toast links to
# Settings can live in ~/.config/eggify/release.env (see scripts/release-env.sh).
#   SKIP_FRONTEND=1     reuse internal/server/frontend/.output instead of rebuilding
#   ALLOW_ADHOC=1       `app` only: allow an ad-hoc build when no Developer ID
#                       identity exists (local testing; never publishable)
#   DMG_STYLE=0         skip the Finder window styling (needs Automation access)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=release-env.sh
source "$ROOT/scripts/release-env.sh"
OUT="$ROOT/dist/wails"
APP="$OUT/Eggify.app"
DMG="$OUT/Eggify.dmg"
VOL_NAME="Eggify"
VERSION="$(sed -n 's/.*Version *= *"\(.*\)".*/\1/p' "$ROOT/internal/version/version.go" | head -1)"
ARCH="$(uname -m)"; [[ "$ARCH" == "x86_64" ]] && ARCH=amd64
PLATFORM="darwin-$ARCH"
ZIP="$OUT/Eggify-$PLATFORM.zip"
MANIFEST="$OUT/latest.json"
BUNDLE_ID="com.eggify.desktop.wails"

APPLE_TEAM_ID="${APPLE_TEAM_ID:-}"
NOTARY_PROFILE="${NOTARY_PROFILE:-}"
DEPLOY_HOST="${DEPLOY_HOST:-}"
DEPLOY_DIR="${DEPLOY_DIR:-}"
PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-}"
PUBLIC_BASE_URL="${PUBLIC_BASE_URL%/}"
RELEASE_PAGE_URL="${RELEASE_PAGE_URL:-${PUBLIC_BASE_URL%/*}/#download}"

# Versioned (immutable) names on the server; Eggify.dmg / Eggify-<platform>.zip
# are stable aliases of the newest release.
V_DMG="Eggify-$VERSION.dmg"
V_ZIP="Eggify-$VERSION-$PLATFORM.zip"

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!  %s\033[0m\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

[[ "$(uname)" == "Darwin" ]] || die "desktop releases are built on macOS"
[[ -n "$VERSION" ]] || die "could not read the version from internal/version/version.go"

# need VAR... : stop with a clear message when a setting is missing.
need() {
  local v
  for v in "$@"; do
    [[ -n "${!v:-}" ]] || die "$v is not set. Export it or put it in ~/.config/eggify/release.env (see scripts/release-env.sh)."
  done
}

find_identity() {
  if [[ -n "${SIGN_IDENTITY:-}" ]]; then
    echo "$SIGN_IDENTITY"; return
  fi
  security find-identity -v -p codesigning 2>/dev/null \
    | grep "Developer ID Application" | grep "($APPLE_TEAM_ID)" | head -1 \
    | sed 's/.*"\(.*\)"/\1/' || true
}

sha_of() { shasum -a 256 "$1" | awk '{print $1}'; }
size_of() { stat -f%z "$1"; }

# ── app ──────────────────────────────────────────────────────────────────────
# release_endpoints: the compiled-in service endpoints. Releases must not ship
# without them (no updater, no default relay). The manifest
# host must equal the host of the ZIP URLs written into latest.json, so both
# default to PUBLIC_BASE_URL. ALLOW_ADHOC=1 (local build) skips the checks.
release_endpoints() {
  [[ "${ALLOW_ADHOC:-}" == "1" ]] && return 0
  need PUBLIC_BASE_URL
  export EGGIFY_BUILD_UPDATE_MANIFEST_URL="${EGGIFY_BUILD_UPDATE_MANIFEST_URL:-$PUBLIC_BASE_URL/latest.json}"
  export EGGIFY_BUILD_DOWNLOAD_URL="${EGGIFY_BUILD_DOWNLOAD_URL:-$PUBLIC_BASE_URL/Eggify.dmg}"
  if [[ "${ALLOW_NO_RELAY:-}" != "1" ]]; then
    need EGGIFY_BUILD_RELAY_ADDR   # ALLOW_NO_RELAY=1 to build without a default relay
  fi
}

cmd_app() {
  [[ "${ALLOW_ADHOC:-}" == "1" ]] || need APPLE_TEAM_ID
  release_endpoints
  local identity; identity="$(find_identity)"
  if [[ -z "$identity" ]]; then
    if [[ "${ALLOW_ADHOC:-}" != "1" ]]; then
      die "no 'Developer ID Application' identity for team $APPLE_TEAM_ID in the keychain.
       Set SIGN_IDENTITY, or ALLOW_ADHOC=1 for a local-only ad-hoc build."
    fi
    warn "################################################################"
    warn "#  NO DEVELOPER ID IDENTITY: building an AD-HOC signed app.     #"
    warn "#  It runs only on this Mac and can never be notarized or       #"
    warn "#  published (the deploy gate refuses it).                      #"
    warn "################################################################"
    unset SIGN_IDENTITY
  else
    say "Signing identity: $identity"
    export SIGN_IDENTITY="$identity"
  fi
  local fresh=1; [[ "${SKIP_FRONTEND:-}" == "1" ]] && fresh=0
  FRESH_FRONTEND=$fresh APPLE_TEAM_ID="$APPLE_TEAM_ID" bash "$ROOT/scripts/build-desktop-wails.sh"

  if [[ -n "${SIGN_IDENTITY:-}" ]]; then
    codesign --verify --deep --strict --verbose=2 "$APP"
    local team; team="$(codesign -dv --verbose=4 "$APP" 2>&1 | sed -n 's/^TeamIdentifier=//p')"
    [[ "$team" == "$APPLE_TEAM_ID" ]] || die "app signed by team '$team', expected '$APPLE_TEAM_ID'"
    local details; details="$(codesign -d --verbose=2 "$APP" 2>&1)"
    [[ "$details" == *"(runtime)"* ]] || die "hardened runtime flag missing"
    say "Signed $APP (team $team, hardened runtime, $VERSION)"
  fi
}

# ── dmg ──────────────────────────────────────────────────────────────────────
style_dmg() {
  local mount="$1"
  [[ "${DMG_STYLE:-1}" == "0" ]] && return 0
  # Finder scripting needs Automation permission; never let it hang the build.
  osascript >/dev/null 2>&1 <<EOF &
tell application "Finder"
  tell disk "$VOL_NAME"
    open
    set current view of container window to icon view
    set toolbar visible of container window to false
    set statusbar visible of container window to false
    set bounds of container window to {100, 100, 640, 440}
    set theViewOptions to the icon view options of container window
    set arrangement of theViewOptions to not arranged
    set icon size of theViewOptions to 96
    set position of item "Eggify.app" of container window to {140, 160}
    set position of item "Applications" of container window to {400, 160}
    close
    open
    update without registering applications
    delay 1
    close
  end tell
end tell
EOF
  local pid=$! i
  for i in $(seq 1 60); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    warn "Finder styling timed out (grant Automation access to style the DMG); continuing unstyled"
  elif ! wait "$pid"; then
    warn "Finder styling failed; continuing with an unstyled DMG"
  fi
  [[ -f "$mount/.DS_Store" ]] && say "DMG window styled"
  return 0
}

# notarytool credentials: the keychain profile, else the App Store Connect
# API key (no keychain needed, e.g. CI or a session without keychain access).
NOTARY_AUTH=()
notary_auth() {
  [[ ${#NOTARY_AUTH[@]} -gt 0 ]] && return 0
  local key="${ASC_KEY_PATH:-$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID:-}.p8}"
  if [[ "${NOTARY_USE_API_KEY:-0}" != 1 ]] \
      && [[ -n "$NOTARY_PROFILE" ]] \
      && xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1; then
    NOTARY_AUTH=(--keychain-profile "$NOTARY_PROFILE")
  elif [[ -n "${ASC_KEY_ID:-}" && -n "${ASC_ISSUER_ID:-}" && -f "$key" ]]; then
    say "Notarizing with the App Store Connect API key $ASC_KEY_ID"
    NOTARY_AUTH=(--key "$key" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID")
  else
    die "no notarytool credentials: keychain profile '${NOTARY_PROFILE:-<unset>}' not found and no App Store Connect key.
       Create the profile: xcrun notarytool store-credentials <profile> --apple-id <id> --team-id $APPLE_TEAM_ID
       or set ASC_KEY_ID, ASC_ISSUER_ID and ASC_KEY_PATH (see scripts/release-env.sh)"
  fi
}

notarize() {
  local file="$1"
  notary_auth
  say "Submitting $(basename "$file") to Apple notary service (takes a few minutes)"
  local out; out="$(mktemp)"
  if ! xcrun notarytool submit "$file" "${NOTARY_AUTH[@]}" --wait \
      --output-format json >"$out"; then
    cat "$out" >&2; rm -f "$out"; die "notarytool submit failed"
  fi
  local id status
  id="$(plutil -extract id raw -o - "$out" 2>/dev/null || true)"
  status="$(plutil -extract status raw -o - "$out" 2>/dev/null || true)"
  rm -f "$out"
  echo "    submission id: $id"
  echo "    status:        $status"
  echo "$id $status" > "$OUT/notarization.txt"
  if [[ "$status" != "Accepted" ]]; then
    [[ -n "$id" ]] && xcrun notarytool log "$id" "${NOTARY_AUTH[@]}" >&2 || true
    die "notarization was not accepted ($status)"
  fi
}

staple() {
  local target="$1" i
  for i in 1 2 3 4 5; do
    if xcrun stapler staple "$target"; then return 0; fi
    [[ $i -eq 5 ]] && return 1
    echo "    staple failed, retrying in 15s ($i/5)..."
    sleep 15
  done
}

cmd_dmg() {
  [[ "${ALLOW_ADHOC:-}" != "1" ]] || die "ALLOW_ADHOC=1 builds are local only: they cannot be packaged or published. Unset it and rebuild."
  need PUBLIC_BASE_URL
  cmd_app
  local identity; identity="$(find_identity)"
  [[ -n "$identity" ]] || die "a DMG for distribution needs a Developer ID identity"

  say "[1/6] Preparing DMG contents"
  local stage="$OUT/dmg-stage" tmp="$OUT/Eggify-tmp.dmg"
  rm -rf "$stage" "$tmp" "$DMG"
  mkdir -p "$stage"
  ditto "$APP" "$stage/Eggify.app"
  ln -s /Applications "$stage/Applications"

  say "[2/6] Creating DMG"
  hdiutil create -volname "$VOL_NAME" -srcfolder "$stage" -ov -format UDRW -fs HFS+ "$tmp" >/dev/null

  say "[3/6] Styling DMG window"
  # Detach anything left mounted under the same name by an earlier run.
  [[ -d "/Volumes/$VOL_NAME" ]] && hdiutil detach "/Volumes/$VOL_NAME" -force -quiet || true
  local mount
  mount="$(hdiutil attach -readwrite -noverify -noautoopen "$tmp" | grep -o '/Volumes/.*$' | head -1)"
  style_dmg "$mount"
  sync
  hdiutil detach "$mount" -quiet || hdiutil detach "$mount" -force

  say "[4/6] Compressing and signing DMG"
  hdiutil convert "$tmp" -format UDZO -imagekey zlib-level=9 -o "$DMG" >/dev/null
  rm -rf "$tmp" "$stage"
  codesign --force --timestamp --sign "$identity" "$DMG"

  say "[5/6] Notarizing"
  notarize "$DMG"
  say "Stapling ticket to the DMG"
  staple "$DMG" || die "could not staple the DMG"
  # Notarizing the DMG notarizes the app inside it; staple the .app too so the
  # update ZIP launches offline (n0 learned this the hard way).
  say "Stapling ticket to the .app"
  staple "$APP" || die "could not staple $APP"

  say "[6/6] Update ZIP, checksums, manifest"
  rm -f "$ZIP"
  ditto -c -k --keepParent "$APP" "$ZIP"
  local dmg_sha zip_sha
  dmg_sha="$(sha_of "$DMG")"; zip_sha="$(sha_of "$ZIP")"
  echo "$dmg_sha  $V_DMG" > "$DMG.sha256"
  echo "$zip_sha  $V_ZIP" > "$ZIP.sha256"
  local min_os
  min_os="$(plutil -extract LSMinimumSystemVersion raw -o - "$APP/Contents/Info.plist")"
  cat > "$MANIFEST" <<EOF
{
  "version": "$VERSION",
  "published_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "release_url": "$RELEASE_PAGE_URL",
  "min_os": "$min_os",
  "platforms": {
    "$PLATFORM": {
      "url": "$PUBLIC_BASE_URL/$V_ZIP",
      "sha256": "$zip_sha",
      "size": $(size_of "$ZIP"),
      "dmg_url": "$PUBLIC_BASE_URL/$V_DMG",
      "dmg_sha256": "$dmg_sha",
      "dmg_size": $(size_of "$DMG")
    }
  }
}
EOF
  plutil -lint -s "$MANIFEST" >/dev/null 2>&1 || python3 -m json.tool "$MANIFEST" >/dev/null
  echo
  say "DMG:      $DMG ($(du -h "$DMG" | cut -f1 | xargs))"
  say "ZIP:      $ZIP ($(du -h "$ZIP" | cut -f1 | xargs))"
  say "Manifest: $MANIFEST"
}

# ── gate ─────────────────────────────────────────────────────────────────────
# Same checks as n0's deploy-dmg: refuse anything that would not pass
# Gatekeeper on a clean Mac, or that the in-app updater would refuse.
cmd_gate() {
  need APPLE_TEAM_ID
  say "Verifying signatures before publish"
  [[ -f "$DMG" ]] || die "DMG not found at $DMG (run: just desktop-dmg)"
  [[ -f "$ZIP" ]] || die "update ZIP not found at $ZIP (run: just desktop-dmg)"
  [[ -f "$MANIFEST" ]] || die "manifest not found at $MANIFEST (run: just desktop-dmg)"

  # The DMG must be signed, notarized and stapled.
  spctl -a -t open --context context:primary-signature -v "$DMG" \
    || die "$DMG not accepted by Gatekeeper — refusing to publish"
  xcrun stapler validate "$DMG" \
    || die "$DMG has no stapled notarization ticket — refusing to publish"

  # The update ZIP is what the in-app updater installs; verify it the same way.
  GATE_DIR="$(mktemp -d)"
  trap 'rm -rf "${GATE_DIR:-}"' EXIT
  local vdir="$GATE_DIR"
  ditto -x -k "$ZIP" "$vdir"
  local vapp; vapp="$(find "$vdir" -maxdepth 2 -name '*.app' | head -1)"
  [[ -n "$vapp" ]] || die "no .app inside $ZIP"
  codesign --verify --deep --strict "$vapp" \
    || die "update bundle signature invalid — refusing to publish"
  spctl --assess --type exec -v "$vapp" \
    || die "update bundle rejected by Gatekeeper (not notarized?) — refusing to publish"
  xcrun stapler validate "$vapp" \
    || die "update bundle has no stapled ticket — refusing to publish"
  local info team ident vver
  info="$(codesign -dv --verbose=4 "$vapp" 2>&1)"
  team="$(sed -n 's/^TeamIdentifier=//p' <<<"$info")"
  ident="$(sed -n 's/^Identifier=//p' <<<"$info")"
  [[ "$team" == "$APPLE_TEAM_ID" ]] || die "update bundle signed by team '$team', expected '$APPLE_TEAM_ID'"
  [[ "$ident" == "$BUNDLE_ID" ]] || die "update bundle id '$ident', expected '$BUNDLE_ID'"
  vver="$(plutil -extract CFBundleShortVersionString raw -o - "$vapp/Contents/Info.plist")"
  [[ "$vver" == "$VERSION" ]] || die "bundle version $vver != $VERSION"

  # The manifest must describe exactly these files.
  local m_ver m_sha m_dsha
  m_ver="$(plutil -extract version raw -o - "$MANIFEST")"
  m_sha="$(plutil -extract "platforms.$PLATFORM.sha256" raw -o - "$MANIFEST")"
  m_dsha="$(plutil -extract "platforms.$PLATFORM.dmg_sha256" raw -o - "$MANIFEST")"
  [[ "$m_ver" == "$VERSION" ]] || die "manifest version $m_ver != $VERSION"
  [[ "$m_sha" == "$(sha_of "$ZIP")" ]] || die "manifest sha256 does not match $ZIP"
  [[ "$m_dsha" == "$(sha_of "$DMG")" ]] || die "manifest dmg_sha256 does not match $DMG"

  say "Gate passed: DMG notarized + stapled; update bundle $ident $vver signed by $team, notarized, stapled"
}

# ── deploy ───────────────────────────────────────────────────────────────────
cmd_deploy() {
  [[ "${ALLOW_ADHOC:-}" != "1" ]] || die "ALLOW_ADHOC=1 builds are local only: they cannot be packaged or published. Unset it and rebuild."
  need DEPLOY_HOST DEPLOY_DIR PUBLIC_BASE_URL
  cmd_gate
  say "Publishing $VERSION to $DEPLOY_HOST:$DEPLOY_DIR"
  ssh "$DEPLOY_HOST" "mkdir -p '$DEPLOY_DIR'"

  # Binary assets first, under versioned names, each uploaded to a .part file
  # and renamed into place so nobody downloads half a file.
  local f
  put() { # <local> <remote name>
    rsync -t --progress "$1" "$DEPLOY_HOST:$DEPLOY_DIR/.$2.part"
    ssh "$DEPLOY_HOST" "chmod 644 '$DEPLOY_DIR/.$2.part' && mv -f '$DEPLOY_DIR/.$2.part' '$DEPLOY_DIR/$2'"
  }
  put "$DMG" "$V_DMG"
  put "$DMG.sha256" "$V_DMG.sha256"
  put "$ZIP" "$V_ZIP"
  put "$ZIP.sha256" "$V_ZIP.sha256"
  put "$MANIFEST" "latest-$VERSION.json"

  # Stable aliases + latest.json last, so the manifest never points at files
  # that are not there yet. Old versioned files stay for rollback.
  ssh "$DEPLOY_HOST" bash -s -- "$DEPLOY_DIR" "$VERSION" "$PLATFORM" <<'REMOTE'
set -euo pipefail
dir="$1"; v="$2"; plat="$3"
cd "$dir"
alias_to() { cp -f "$1" ".$2.tmp" && chmod 644 ".$2.tmp" && mv -f ".$2.tmp" "$2"; }
alias_to "Eggify-$v.dmg" "Eggify.dmg"
alias_to "Eggify-$v.dmg.sha256" "Eggify.dmg.sha256"
alias_to "Eggify-$v-$plat.zip" "Eggify-$plat.zip"
alias_to "Eggify-$v-$plat.zip.sha256" "Eggify-$plat.zip.sha256"
alias_to "latest-$v.json" "latest.json"
ls -la "$dir"
REMOTE
  cmd_verify
}

# ── verify ───────────────────────────────────────────────────────────────────
cmd_verify() {
  need PUBLIC_BASE_URL
  say "Verifying $PUBLIC_BASE_URL"
  local url code fail=0
  for f in latest.json "latest-$VERSION.json" Eggify.dmg Eggify.dmg.sha256 "$V_DMG" "$V_DMG.sha256" \
           "Eggify-$PLATFORM.zip" "$V_ZIP" "$V_ZIP.sha256"; do
    url="$PUBLIC_BASE_URL/$f"
    code="$(curl -s -o /dev/null -I -w '%{http_code}' -m 30 "$url")"
    printf '    %s %s\n' "$code" "$url"
    [[ "$code" == "200" ]] || fail=1
  done
  [[ $fail -eq 0 ]] || die "some published files are not reachable"
  local remote_ver
  remote_ver="$(curl -fsS -m 30 "$PUBLIC_BASE_URL/latest.json" | plutil -extract version raw -o - - 2>/dev/null || true)"
  [[ "$remote_ver" == "$VERSION" ]] || die "published latest.json says version '$remote_ver', expected $VERSION"
  say "Published: $PUBLIC_BASE_URL/Eggify.dmg (version $remote_ver)"
}

# ── rollback ─────────────────────────────────────────────────────────────────
# Points latest.json and the stable aliases back at an earlier release that is
# still on the server. Clients never downgrade themselves (the updater only
# installs newer versions); this stops further installs of a bad release.
cmd_rollback() {
  need DEPLOY_HOST DEPLOY_DIR
  local v="${1:-}"
  [[ -n "$v" ]] || die "usage: $0 rollback <version>"
  ssh "$DEPLOY_HOST" bash -s -- "$DEPLOY_DIR" "$v" "$PLATFORM" <<'REMOTE'
set -euo pipefail
dir="$1"; v="$2"; plat="$3"
cd "$dir"
for f in "Eggify-$v.dmg" "Eggify-$v-$plat.zip" "latest-$v.json"; do
  [[ -f "$f" ]] || { echo "ERROR: $dir/$f not found; available:"; ls "$dir"; exit 1; }
done
alias_to() { cp -f "$1" ".$2.tmp" && chmod 644 ".$2.tmp" && mv -f ".$2.tmp" "$2"; }
alias_to "Eggify-$v.dmg" "Eggify.dmg"
alias_to "Eggify-$v.dmg.sha256" "Eggify.dmg.sha256"
alias_to "Eggify-$v-$plat.zip" "Eggify-$plat.zip"
alias_to "Eggify-$v-$plat.zip.sha256" "Eggify-$plat.zip.sha256"
alias_to "latest-$v.json" "latest.json"
echo "latest.json now points at $v"
REMOTE
}

case "${1:-}" in
  app) cmd_app ;;
  dmg) cmd_dmg ;;
  gate) cmd_gate ;;
  deploy) cmd_deploy ;;
  verify) cmd_verify ;;
  rollback) shift; cmd_rollback "$@" ;;
  *) sed -n '2,30p' "$0"; exit 2 ;;
esac
