#!/usr/bin/env bash
# Build the landing page and publish it to your web host.
# Needs DEPLOY_HOST (ssh target, e.g. user@your-host) and LANDING_DEPLOY_DIR
# (the site root served as static files, e.g. /var/www/example), from the
# environment or ~/.config/eggify/release.env.
set -euo pipefail
cd "$(dirname "$0")"
source ../scripts/release-env.sh
HOST=${DEPLOY_HOST:?Set DEPLOY_HOST (ssh target, e.g. user@your-host)}
DEST=${LANDING_DEPLOY_DIR:?Set LANDING_DEPLOY_DIR (site root on the host, e.g. /var/www/example)}

[ -n "${PUBLIC_LICENSE_URL:-}" ] || echo "WARNING: PUBLIC_LICENSE_URL is unset: the interest form will say the server cannot be reached" >&2
[ -n "${PUBLIC_DOWNLOAD_BASE_URL:-}" ] || echo "NOTE: PUBLIC_DOWNLOAD_BASE_URL is unset: download links are site-relative (/download/...)" >&2
npm ci --no-audit --no-fund
npm run build
ssh "$HOST" "mkdir -p '$DEST'"
# /download/ holds the desktop releases (scripts/desktop-release.sh); never delete it.
rsync -rlt --delete --exclude '/download/' --exclude '/media/' dist/ "$HOST:$DEST/"
ssh "$HOST" "find '$DEST' -type d -exec chmod 755 {} + && find '$DEST' -type f -exec chmod 644 {} +"
echo "Deployed to $HOST:$DEST"
