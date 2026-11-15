#!/usr/bin/env bash
# Build one learning video from its episode.json, shots and narration:
#   scripts/build-episode.sh <slug>
#   PLACEHOLDERS=1 scripts/build-episode.sh <slug>   trial build: lines without a wav stay silent
# 1. timing.json from the narration wavs (a line without one is an error)
# 2. render       → out/<slug>.mp4          (holds the "render" lock)
# 3. web version  → out/<slug>-web.mp4      (x264 crf 23, faststart, -16 LUFS, AAC 160k)
# 4. poster       → out/<slug>-poster.jpg   (the title card, without the caption)
# Nothing is uploaded; scripts/deploy.sh does that.
set -euo pipefail
cd "$(dirname "$0")/.."
SLUG=${1:-}
[[ "$SLUG" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || { echo "Usage: scripts/build-episode.sh <slug>" >&2; exit 2; }
FFMPEG=$(command -v /opt/homebrew/bin/ffmpeg || command -v ffmpeg)
COMP=learn-$SLUG
RENDER=out/$SLUG.mp4
VIDEO=out/$SLUG-web.mp4
POSTER=out/$SLUG-poster.jpg
mkdir -p out

node scripts/timing.mjs "$SLUG" ${PLACEHOLDERS:+--estimate}
if node scripts/placeholders.mjs "$SLUG" --list | grep -q .; then
  echo "Warning: this build uses placeholder files (node scripts/placeholders.mjs $SLUG --list)" >&2
fi

scripts/with-lock.sh render npx remotion render "$COMP" "$RENDER" --log=error

# Web version: loudness-normalised (the narration is quiet out of TTS) and faststart, so it
# plays before it has fully downloaded. Same settings as promo/deploy.sh.
"$FFMPEG" -v error -y -i "$RENDER" -c:v libx264 -crf 23 -preset slow -pix_fmt yuv420p -movflags +faststart \
  -af "loudnorm=I=-16:TP=-1.5:LRA=11" -ar 48000 -c:a aac -b:a 160k "$VIDEO"

# Poster: the title card once everything has landed, without the caption strip.
scripts/with-lock.sh render npx remotion still "$COMP" "$POSTER" --frame="${POSTER_FRAME:-100}" \
  --props='{"captions":false}' --image-format=jpeg --jpeg-quality=85 --log=error

ls -lh "$RENDER" "$VIDEO" "$POSTER"
