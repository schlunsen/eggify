#!/usr/bin/env bash
# Throwaway Eggify server for the iOS UI tests (eggifyUITests).
#
# Builds the server, runs it with a temporary HOME on http://localhost:3399
# (plain HTTP on loopback, which the app allows), creates the test user and
# seeds the demo session the walkthrough opens ("Fix login bug").
#
#   scripts/ios-e2e-server.sh            # start, print the env for xcodebuild, wait
#   scripts/ios-e2e-server.sh --test     # start, run the UI tests, stop
#
# Env: E2E_PORT [3399], E2E_DEVICE (simulator id or name for --test),
#      SKIP_FRONTEND=1 to reuse internal/server/frontend/.output.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${E2E_PORT:-3399}"
USER_NAME="tester"
PASSWORD="eggify-e2e-$(date +%s)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eggify-e2e.XXXXXX")"
BIN="$WORK/eggify"
LOG="$WORK/server.log"
BASE="http://localhost:$PORT"

say() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
  [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null || true
  [[ -n "${SERVER_PID:-}" ]] && wait "$SERVER_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

if [[ "${SKIP_FRONTEND:-0}" != 1 || ! -d "$ROOT/internal/server/frontend/.output/public" ]]; then
  say "Building the frontend"
  (cd "$ROOT/internal/server/frontend" && { [[ -d node_modules ]] || npm ci --silent; } && npm run generate >/dev/null)
fi
say "Building the server"
(cd "$ROOT" && go build -o "$BIN" ./cmd/eggify)

say "Starting on $BASE (data in $WORK)"
mkdir -p "$WORK/home"
HOME="$WORK/home" EGGIFY_PORT="$PORT" EGGIFY_DISABLE_TLS=true EGGIFY_SECRET_STORE=file EGGIFY_NO_BROWSER=1 \
  "$BIN" >"$LOG" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 60); do
  curl -fsS "$BASE/api/health" >/dev/null 2>&1 && break
  kill -0 "$SERVER_PID" 2>/dev/null || { tail -20 "$LOG" >&2; die "server exited"; }
  sleep 0.5
done
curl -fsS "$BASE/api/health" >/dev/null || { tail -20 "$LOG" >&2; die "server did not come up"; }

say "Creating user $USER_NAME"
curl -fsS -X POST "$BASE/api/auth/setup" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER_NAME\",\"password\":\"$PASSWORD\"}" >/dev/null

say "Seeding the demo session"
DB="$WORK/home/.claude/eggify/eggify.db"
[[ -f "$DB" ]] || die "database not found at $DB"
SID="11111111-2222-4333-8444-555555555555"
for i in 1 2 3 4 5 6 7 8; do printf -v "M$i" "%s" "$(uuidgen | tr A-Z a-z)"; done
tool() { printf '{"id":"%s","name":"%s","input":%s}' "$1" "$2" "$3"; }
TOOLS_READ="[$(tool t1 Read '{"file_path":"/tmp/demo/auth.go"}')]"
TOOLS_GREP="[$(tool t2 Grep '{"pattern":"password","path":"/tmp/demo"}')]"
TOOLS_BASH="[$(tool t3 Bash '{"command":"go test ./...","description":"Run the tests"}')]"
TOOLS_GLOB="[$(tool t4 Glob '{"pattern":"**/*_test.go"}')]"
TOOLS_EDIT="[$(tool t5 Edit '{"file_path":"/tmp/demo/auth.go","old_string":"if pw == nil {\n\treturn nil\n}","new_string":"if pw == \"\" {\n\treturn ErrEmptyPassword\n}"}')]"
sqlite3 "$DB" <<SQL
INSERT INTO agent_sessions (id, status, model_name, provider, options, message_count, cost_usd, num_turns)
VALUES ('$SID', 'idle', 'claude-sonnet-5-5', 'claude',
        '{"agent_name":"Fix login bug","working_directory":"/tmp/demo","model":"claude-sonnet-5-5"}', 8, 0.42, 3);
INSERT INTO agent_messages (id, session_id, sequence, role, content, tool_uses) VALUES
 ('$M1', '$SID', 1, 'user', 'The login endpoint accepts empty passwords. Fix it and add a test.', NULL),
 ('$M2', '$SID', 2, 'assistant', '', '$TOOLS_READ'),
 ('$M3', '$SID', 3, 'assistant', '', '$TOOLS_GREP'),
 ('$M4', '$SID', 4, 'assistant', '', '$TOOLS_GLOB'),
 ('$M5', '$SID', 5, 'assistant', '', '$TOOLS_BASH'),
 ('$M6', '$SID', 6, 'assistant', 'Found it: \`Login\` only checks for a nil password. See https://example.com/owasp for why this matters.', NULL),
 ('$M7', '$SID', 7, 'assistant', '', '$TOOLS_EDIT'),
 ('$M8', '$SID', 8, 'assistant', '**Done.** Empty passwords now return an error, and \`TestLoginEmptyPassword\` covers it.

\`\`\`go
if pw == "" {
	return ErrEmptyPassword
}
\`\`\`', NULL);
SQL

cat <<EOF

  TEST_RUNNER_EGGIFY_URL=http://localhost:$PORT
  TEST_RUNNER_EGGIFY_USER=$USER_NAME
  TEST_RUNNER_EGGIFY_PASSWORD=$PASSWORD

EOF

if [[ "${1:-}" == "--test" ]]; then
  DEVICE="${E2E_DEVICE:-iPhone 17 Pro}"
  if [[ "$DEVICE" =~ ^[0-9A-F-]{36}$ ]]; then DEST="platform=iOS Simulator,id=$DEVICE"; else DEST="platform=iOS Simulator,name=$DEVICE"; fi
  say "Running the UI tests on $DEVICE"
  SHOTS="${E2E_SHOTS:-$WORK/shots}"; mkdir -p "$SHOTS"
  TEST_RUNNER_EGGIFY_URL="http://localhost:$PORT" TEST_RUNNER_EGGIFY_USER="$USER_NAME" \
  TEST_RUNNER_EGGIFY_PASSWORD="$PASSWORD" TEST_RUNNER_EGGIFY_SHOTS="$SHOTS" \
    xcodebuild test -project "$ROOT/ios/eggify/eggify.xcodeproj" -scheme eggify \
      -destination "$DEST" -only-testing:eggifyUITests
  exit $?
fi

say "Server running (Ctrl-C to stop). Log: $LOG"
wait "$SERVER_PID"
