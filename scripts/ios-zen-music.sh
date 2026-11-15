#!/bin/sh
# Recodes the web Zen music (internal/server/frontend/public/zen/audio/*.mp3) to
# 96 kbps AAC for the iOS app (ios/eggify/eggify/Resources/ZenMusic). macOS only
# (afconvert). Run after adding or changing a track: sh scripts/ios-zen-music.sh
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/internal/server/frontend/public/zen/audio"
DST="$ROOT/ios/eggify/eggify/Resources/ZenMusic"
mkdir -p "$DST"
rm -f "$DST"/*.m4a "$DST"/*.beats.json
for mp3 in "$SRC"/*.mp3; do
  name="$(basename "$mp3" .mp3)"
  afconvert -f m4af -d aac -b 96000 "$mp3" "$DST/$name.m4a"
  cp "$SRC/$name.beats.json" "$DST/$name.beats.json"
done
du -sh "$DST"
