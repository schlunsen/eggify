#!/usr/bin/env bash
# Demo server for the screen library: ../demo-server.sh's recipe, with two differences.
#   - HOME is /Users/Shared/praline (a sibling name on another port): a real path, not the /tmp
#     symlink, so the app shortens project folders to "~/projects/teashop-api", and a short
#     one where the app shows it in full (/Users/Shared/praline/projects/teashop-api).
#   - after the base seed (../seed.mjs) it runs seed-extra.mjs.
#
#   learn/capture/library/server.sh              # port 3420; Ctrl-C stops and deletes it
#   learn/capture/library/server.sh --port 3421
# Writes /tmp/eggify-learn-<port>/server.json like demo-server.sh, so ../lib.mjs readServer() works.
# Needs the binary demo-server.sh built (learn/capture/bin/eggify); run that once first if missing.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
CAPTURE="$(cd "$HERE/.." && pwd)"
PORT=3420
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
DEMO_USER="praline"
WORK="/private/tmp/eggify-learn-$PORT"
# The demo home is a believable Mac path, never a temporary folder: paths on screen read
# ~/projects/<name>, or /Users/Shared/praline/projects/<name> where the app shows them in full.
# Servers on other ports get a sibling name, so several can run at once.
demo_home() {
  local names=(praline praline mrpraline epraline praline.e mr.praline e.praline pralines praline.mr ericpraline)
  echo "/Users/Shared/${names[$(( $1 % 10 ))]}"
}
DEMO_HOME="$(demo_home "$PORT")"
BIN="$CAPTURE/bin/eggify"
LOG="$WORK/server.log"
BASE="http://localhost:$PORT"
say() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }
[[ -x "$BIN" ]] || die "no server binary at $BIN; run learn/capture/demo-server.sh once to build it"
if curl -fsS "$BASE/api/health" >/dev/null 2>&1; then die "something already answers on $BASE"; fi
cleanup() {
  [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null || true
  [[ -n "${SERVER_PID:-}" ]] && wait "$SERVER_PID" 2>/dev/null || true
  rm -rf "$WORK" "$DEMO_HOME"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
rm -rf "$WORK" "$DEMO_HOME"
mkdir -p "$DEMO_HOME" "$WORK/tmp"
PASSWORD="norwegian-blue-$(openssl rand -hex 4)"
say "Starting on $BASE (data in $WORK)"
# Empty environment: no keys, tokens or names of the real machine reach the demo server.
env -i HOME="$DEMO_HOME" USER="$DEMO_USER" LOGNAME="$DEMO_USER" LANG=en_US.UTF-8 TMPDIR="$WORK/tmp" \
  PATH="/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin" \
  EGGIFY_PORT="$PORT" EGGIFY_DISABLE_TLS=true EGGIFY_SECRET_STORE=file EGGIFY_NO_BROWSER=1 \
  "$BIN" >"$LOG" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 80); do
  curl -fsS "$BASE/api/health" >/dev/null 2>&1 && break
  kill -0 "$SERVER_PID" 2>/dev/null || { tail -20 "$LOG" >&2; die "server exited"; }
  sleep 0.5
done
curl -fsS "$BASE/api/health" >/dev/null || { tail -20 "$LOG" >&2; die "server did not come up"; }
JSON=$(printf '{"url":"%s","port":%s,"user":"%s","password":"%s","home":"%s","db":"%s","fresh":false,"pid":%s}' \
  "$BASE" "$PORT" "$DEMO_USER" "$PASSWORD" "$DEMO_HOME" "$DEMO_HOME/.claude/eggify/eggify.db" "$SERVER_PID")
(umask 077; printf '%s\n' "$JSON" >"$WORK/server.json")
say "Base seed"
node "$CAPTURE/seed.mjs" --port "$PORT" >&2
say "Library seed"
node "$HERE/seed-extra.mjs" --port "$PORT" >&2
echo "DEMO_SERVER $JSON"
say "Server running (Ctrl-C to stop). Log: $LOG"
wait "$SERVER_PID"
