#!/usr/bin/env bash
# Throwaway Eggify server for the learning videos' screen captures.
#
# Builds the frontend and the server once, runs the server with a temporary HOME
# and an empty environment on http://localhost:<port> (plain HTTP on loopback),
# creates the demo user and seeds the fictional demo workspace (seed.mjs).
#
#   learn/capture/demo-server.sh                 # port 3401, seeded, Ctrl-C stops and deletes it
#   learn/capture/demo-server.sh --port 3402     # one server per episode: 3400 + episode number
#   learn/capture/demo-server.sh --fresh         # no user yet: the first-run /setup page is shown
#   learn/capture/demo-server.sh --keep          # keep /tmp/eggify-learn-<port> after exit
#   learn/capture/demo-server.sh --rebuild       # rebuild the frontend and the binary first
#   learn/capture/demo-server.sh --stop [--port N]   # stop a server started elsewhere (e.g. in the background)
#
# When it is up it writes /tmp/eggify-learn-<port>/server.json
#   {"url","port","user","password","home","db","fresh","pid"}
# and prints the same JSON on a line starting with "DEMO_SERVER ".
# A --fresh server is seeded later with:  node learn/capture/seed.mjs --port <port>
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
PORT=3401 KEEP=0 FRESH=0 REBUILD=0 STOP=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --fresh) FRESH=1; shift ;;
    --rebuild) REBUILD=1; shift ;;
    --stop) STOP=1; shift ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

DEMO_USER="praline"                      # Mr. Praline; shown in the app's header and in paths
WORK="/tmp/eggify-learn-$PORT"
# The demo home is a believable Mac path, never a temporary folder: paths on screen read
# ~/projects/<name>, or /Users/Shared/praline/projects/<name> where the app shows them in full.
# Servers on other ports get a sibling name, so several can run at once.
demo_home() {
  local names=(praline praline mrpraline epraline praline.e mr.praline e.praline pralines praline.mr ericpraline)
  echo "/Users/Shared/${names[$(( $1 % 10 ))]}"
}
DEMO_HOME="$(demo_home "$PORT")"
BIN="$HERE/bin/eggify"
FRONTEND="$ROOT/internal/server/frontend"
LOG="$WORK/server.log"
INFO="$WORK/server.json"
BASE="http://localhost:$PORT"

say() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

if [[ $STOP == 1 ]]; then   # stop the server of this port; its own script then cleans up
  [[ -f "$INFO" ]] || die "no demo server on port $PORT"
  kill "$(sed -E 's/.*"pid":([0-9]+).*/\1/' "$INFO")" 2>/dev/null || true
  exit 0
fi

if curl -fsS "$BASE/api/health" >/dev/null 2>&1; then die "something already answers on $BASE"; fi

# --- build (once) -----------------------------------------------------------
if [[ $REBUILD == 1 || ! -f "$FRONTEND/.output/public/index.html" || ! -d "$FRONTEND/.output/public/_nuxt" ]]; then
  say "Building the frontend (npm ci + npm run generate)"
  (cd "$FRONTEND" && { [[ -d node_modules ]] || npm ci --silent; } && npm run generate >/dev/null)
  REBUILD=1   # the binary embeds the frontend
fi
if [[ $REBUILD == 1 || ! -x "$BIN" ]]; then
  say "Building the server into learn/capture/bin/eggify"
  mkdir -p "$HERE/bin"
  if ! (cd "$ROOT" && go build -o "$BIN" ./cmd/eggify) 2>"$HERE/bin/build.log"; then
    if grep -q "unknown architecture" "$HERE/bin/build.log"; then
      X=/Applications/Xcode.app/Contents/Developer
      (cd "$ROOT" && DEVELOPER_DIR=$X CC=$X/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang \
        CXX=$X/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang++ \
        SDKROOT=$X/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk go build -o "$BIN" ./cmd/eggify)
    else
      cat "$HERE/bin/build.log" >&2; die "go build failed"
    fi
  fi
fi

# --- start --------------------------------------------------------------------
cleanup() {
  [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null || true
  [[ -n "${SERVER_PID:-}" ]] && wait "$SERVER_PID" 2>/dev/null || true
  if [[ $KEEP == 1 ]]; then say "Kept $WORK and $DEMO_HOME"; else rm -rf "$WORK" "$DEMO_HOME"; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

rm -rf "$WORK" "$DEMO_HOME"
mkdir -p "$WORK" "$DEMO_HOME"
PASSWORD="norwegian-blue-$(openssl rand -hex 4)"

say "Starting on $BASE (data in $WORK)"
# An empty environment: no provider keys, tokens or names from the real machine
# reach the demo server. PATH has git and sqlite3 but no agent CLIs.
cd "$DEMO_HOME"   # the server offers its working directory as the default session folder
env -i HOME="$DEMO_HOME" USER="$DEMO_USER" LOGNAME="$DEMO_USER" LANG=en_US.UTF-8 TMPDIR="$WORK/tmp" \
  PATH="/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin" \
  EGGIFY_PORT="$PORT" EGGIFY_DISABLE_TLS=true EGGIFY_SECRET_STORE=file EGGIFY_NO_BROWSER=1 \
  "$BIN" >"$LOG" 2>&1 &
SERVER_PID=$!
mkdir -p "$WORK/tmp"
for _ in $(seq 1 80); do
  curl -fsS "$BASE/api/health" >/dev/null 2>&1 && break
  kill -0 "$SERVER_PID" 2>/dev/null || { tail -20 "$LOG" >&2; die "server exited"; }
  sleep 0.5
done
curl -fsS "$BASE/api/health" >/dev/null || { tail -20 "$LOG" >&2; die "server did not come up"; }

FRESH_JSON=$([[ $FRESH == 1 ]] && echo true || echo false)
JSON=$(printf '{"url":"%s","port":%s,"user":"%s","password":"%s","home":"%s","db":"%s","fresh":%s,"pid":%s}' \
  "$BASE" "$PORT" "$DEMO_USER" "$PASSWORD" "$DEMO_HOME" "$DEMO_HOME/.claude/eggify/eggify.db" "$FRESH_JSON" "$SERVER_PID")
(umask 077; printf '%s\n' "$JSON" >"$INFO")

if [[ $FRESH == 0 ]]; then
  say "Creating user $DEMO_USER and seeding the demo workspace"
  node "$HERE/seed.mjs" --port "$PORT" >&2
fi

echo "DEMO_SERVER $JSON"
say "Server running (Ctrl-C to stop). Log: $LOG"
wait "$SERVER_PID"
