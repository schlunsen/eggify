#!/usr/bin/env bash
# Assets for the engine test episode (id learn-demo, only registered with REMOTION_LEARN_DEV=1):
# screens borrowed from the film, a short clip, estimated (silent) narration.
#   episodes/00-demo/setup.sh && REMOTION_LEARN_DEV=1 npx remotion studio
set -euo pipefail
cd "$(dirname "$0")/../.."
FFMPEG=$(command -v /opt/homebrew/bin/ffmpeg || command -v ffmpeg)
OUT=public/episodes/demo
mkdir -p "$OUT/shots" "$OUT/clips"
# The film's shots are 2000×1250; take the top 16:9 and scale to a 2× capture.
for pair in session:02-session overview:03-overview; do
  "$FFMPEG" -v error -y -i "../promo/public/shots/${pair#*:}.png" -vf "crop=2000:1125:0:0,scale=3840:2160:flags=lanczos" "$OUT/shots/${pair%%:*}.png"
done
cat >"$OUT/shots/session.json" <<'JSON'
{"boxes": {
  "sessions": {"x": 326, "y": 80, "w": 340, "h": 600},
  "newSession": {"x": 336, "y": 139, "w": 321, "h": 43},
  "bashCard": {"x": 720, "y": 504, "w": 758, "h": 92},
  "model": {"x": 1542, "y": 286, "w": 345, "h": 48}
}}
JSON
"$FFMPEG" -v error -y -i ../promo/public/video/zen.mp4 -t 5 -an -vf "scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080" \
  -c:v libx264 -crf 20 -pix_fmt yuv420p "$OUT/clips/zen.mp4"
node scripts/placeholders.mjs demo
