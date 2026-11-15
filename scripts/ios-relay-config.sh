#!/usr/bin/env bash
# Point the iOS app's App Transport Security exception at a relay's wildcard
# domain (the "r.example.com" in https://<id>.r.example.com).
#
#   scripts/ios-relay-config.sh r.example.com
#
# Writes ios/eggify/Info.local.plist (Info.plist with that domain) and
# ios/eggify/Local.xcconfig, which makes Xcode build with it. Both are
# gitignored. Run it again after changing Info.plist.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOMAIN="${1:-}"
PLACEHOLDER="relay-not-configured.invalid"

if [[ ! "$DOMAIN" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ || "$DOMAIN" != *.* ]]; then
  echo "usage: $0 <relay wildcard domain, e.g. r.example.com>" >&2
  exit 1
fi

SRC="$ROOT/ios/eggify/Info.plist"
OUT="$ROOT/ios/eggify/Info.local.plist"
grep -q "<key>$PLACEHOLDER</key>" "$SRC" || { echo "ERROR: $SRC has no $PLACEHOLDER key" >&2; exit 1; }
sed "s|<key>$PLACEHOLDER</key>|<key>$DOMAIN</key>|" "$SRC" > "$OUT"
plutil -lint "$OUT" >/dev/null
printf '// Written by scripts/ios-relay-config.sh\nEGGIFY_INFOPLIST_FILE = Info.local.plist\n' > "$ROOT/ios/eggify/Local.xcconfig"
echo "ATS exception for $DOMAIN: $OUT"
