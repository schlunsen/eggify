#!/usr/bin/env bash
# Run a command while holding a named lock, so heavy jobs (voice generation on the GPU,
# Remotion renders) from several agents never run at the same time.
#   scripts/with-lock.sh tts uv run python gen.py …
set -euo pipefail
NAME=$1; shift
LOCK="${TMPDIR:-/tmp}/eggify-learn-$NAME.lock"
until mkdir "$LOCK" 2>/dev/null; do
  # A lock whose owner is gone is taken over.
  PID=$(cat "$LOCK/pid" 2>/dev/null || true)
  if [[ -n "$PID" ]] && ! kill -0 "$PID" 2>/dev/null; then rm -rf "$LOCK"; continue; fi
  sleep 2
done
echo $$ >"$LOCK/pid"
trap 'rm -rf "$LOCK"' EXIT
"$@"
