#!/usr/bin/env bash
# Publish the rendered promo film to <site root>/media/ (shown on the landing page).
# Needs DEPLOY_HOST and PROMO_DEPLOY_DIR (e.g. /var/www/example/media); also PUBLIC_SITE_URL for the final message.
#   npm run render && ./deploy.sh
set -euo pipefail
cd "$(dirname "$0")"
source ../scripts/release-env.sh
HOST=${DEPLOY_HOST:?Set DEPLOY_HOST (ssh target, e.g. user@your-host)}
DEST=${PROMO_DEPLOY_DIR:?Set PROMO_DEPLOY_DIR (media folder on the host, e.g. /var/www/example/media)}
RENDER=out/eggify-promo.mp4
VIDEO=out/eggify-promo-web.mp4
POSTER=out/eggify-promo-poster.jpg
[ -f "$RENDER" ] || { echo "Render first: npm run render" >&2; exit 1; }
# Web version: loudness-normalised (-16 LUFS, the narration is quiet out of TTS),
# faststart so it plays before it has fully downloaded.
ffmpeg -v error -y -i "$RENDER" -c:v libx264 -crf 23 -preset slow -pix_fmt yuv420p -movflags +faststart \
  -af "loudnorm=I=-16:TP=-1.5:LRA=11" -ar 48000 -c:a aac -b:a 160k "$VIDEO"
# Poster: the end card, where the wordmark and eggify.xyz are on screen.
npx remotion still Promo "$POSTER" --frame=1620 --image-format=jpeg --jpeg-quality=85 --log=error
ssh "$HOST" "mkdir -p '$DEST'"
# Upload under a temporary name, then rename, so nobody streams a half-written file.
rsync -t "$VIDEO" "$HOST:$DEST/.eggify-promo.mp4.part"
rsync -t "$POSTER" "$HOST:$DEST/eggify-promo-poster.jpg"
ssh "$HOST" "mv '$DEST/.eggify-promo.mp4.part' '$DEST/eggify-promo.mp4' && chmod 644 '$DEST'/*"
echo "Published ${PUBLIC_SITE_URL:-<your site>}/media/eggify-promo.mp4"
