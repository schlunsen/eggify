#!/usr/bin/env bash
# Publish one learning video and its poster to <media folder>/learn/ on the web host (shown on /learn).
# Needs DEPLOY_HOST and PROMO_DEPLOY_DIR (the site's media folder; or LEARN_DEPLOY_DIR for the
# learn folder itself), from the environment or ~/.config/eggify/release.env.
#   scripts/deploy.sh <slug> [--dry-run]
# Expects learn/out/<slug>-web.mp4 and learn/out/<slug>-poster.jpg (made by the build script).
# Afterwards set `published: true` for the episode in landing/src/data/learn.ts and run
# landing/deploy.sh.
set -euo pipefail
cd "$(dirname "$0")/.."
source ../scripts/release-env.sh
HOST=${DEPLOY_HOST:?Set DEPLOY_HOST (ssh target, e.g. user@your-host)}
DEST=${LEARN_DEPLOY_DIR:-${PROMO_DEPLOY_DIR:?Set PROMO_DEPLOY_DIR (media folder on the host, e.g. /var/www/example/media)}/learn}

usage() { echo "Usage: scripts/deploy.sh <slug> [--dry-run]" >&2; exit 2; }

SLUG=
DRY=0
for arg in "$@"; do
  case "$arg" in
    --dry-run|-n) DRY=1 ;;
    -*) usage ;;
    *) [ -z "$SLUG" ] || usage; SLUG=$arg ;;
  esac
done
[ -n "$SLUG" ] || usage
# The slug becomes a file name on the server and part of a remote command.
[[ "$SLUG" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || { echo "Not a slug (lower-case letters, digits and dashes): $SLUG" >&2; exit 2; }

VIDEO=out/$SLUG-web.mp4
POSTER=out/$SLUG-poster.jpg
missing=0
for f in "$VIDEO" "$POSTER"; do
  [ -s "$f" ] || { echo "Missing: learn/$f (build the episode first)" >&2; missing=1; }
done
[ "$missing" = 0 ] || exit 1

run() {
  if [ "$DRY" = 1 ]; then
    printf 'would run:'; printf ' %q' "$@"; printf '\n'
  else
    "$@"
  fi
}

run ssh "$HOST" "mkdir -p '$DEST' && chmod 755 '$DEST'"
# Upload under temporary names, then rename, so nobody streams a half-written file.
run rsync -t "$VIDEO" "$HOST:$DEST/.$SLUG.mp4.part"
run rsync -t "$POSTER" "$HOST:$DEST/.$SLUG-poster.jpg.part"
run ssh "$HOST" "mv '$DEST/.$SLUG.mp4.part' '$DEST/$SLUG.mp4' && mv '$DEST/.$SLUG-poster.jpg.part' '$DEST/$SLUG-poster.jpg' && chmod 644 '$DEST/$SLUG.mp4' '$DEST/$SLUG-poster.jpg'"

if [ "$DRY" = 1 ]; then
  echo "Dry run: nothing was uploaded. It would publish https://eggify.xyz/media/learn/$SLUG.mp4"
else
  echo "Published https://eggify.xyz/media/learn/$SLUG.mp4"
  echo "Poster    https://eggify.xyz/media/learn/$SLUG-poster.jpg"
  echo "Next: set published: true for '$SLUG' in landing/src/data/learn.ts, then run landing/deploy.sh"
fi
