# Sourced by the release scripts. Loads local release settings (App Store
# Connect key, notary profile, deploy host…) from a file outside the repo, so
# nothing secret is committed. Variables already set in the environment win.
#
#   ~/.config/eggify/release.env   (override with EGGIFY_RELEASE_ENV=/path)
#
# Format: KEY=VALUE per line; whole-line and ` # trailing` comments allowed. Example:
#   ASC_KEY_ID=ABC123XYZ9
#   ASC_ISSUER_ID=00000000-0000-0000-0000-000000000000
#   ASC_KEY_PATH=~/.appstoreconnect/private_keys/AuthKey_ABC123XYZ9.p8
#   NOTARY_PROFILE=my-notary-profile
#   APPLE_TEAM_ID=ABCDE12345
#   DEPLOY_HOST=user@your-host
#   DEPLOY_DIR=/var/www/example/download
#   PUBLIC_BASE_URL=https://example.com/download
#
# Endpoints compiled into release builds (scripts/build-desktop-wails.sh passes
# them as -ldflags -X; a plain source build has none and the feature is off):
#   EGGIFY_BUILD_RELAY_ADDR=relay.example.com:8443   # Eggify relay for remote access
#   EGGIFY_BUILD_UPDATE_MANIFEST_URL=https://updates.example.com/latest.json
#   EGGIFY_BUILD_DOWNLOAD_URL=https://example.com/download/Eggify.dmg
#   EGGIFY_BUILD_RELAY_ATS_DOMAIN=r.example.com      # iOS: ATS exception for the relay's wildcard
#                                                    # domain (scripts/ios-relay-config.sh)
load_release_env() {
  local file="${EGGIFY_RELEASE_ENV:-$HOME/.config/eggify/release.env}"
  [[ -f "$file" ]] || return 0
  local line key value
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[[:space:]]*# ]] && continue   # whole-line comment
    line="${line%%[[:space:]]#*}"                 # trailing " # comment"; a # inside a value stays
    [[ "$line" =~ ^[[:space:]]*([A-Z_][A-Z0-9_]*)=(.*)$ ]] || continue
    key="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"
    value="${value%"${value##*[![:space:]]}"}"   # trim trailing space
    value="${value/#\~/$HOME}"
    [[ -n "${!key:-}" ]] || export "$key=$value"
  done < "$file"
}
load_release_env
