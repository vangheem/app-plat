#!/usr/bin/env bash
# End-to-end check for a running app-plat install.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API="${APP_PLAT_API_URL:-http://api.127.0.0.1.nip.io:30080}"
NS="${APP_PLAT_NAMESPACE:-app-plat}"
ADMIN_EMAIL="${APP_PLAT_ADMIN_EMAIL:-admin@app-plat.local}"
ADMIN_PASSWORD="${APP_PLAT_ADMIN_PASSWORD:-app-plat-admin}"

fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "OK: $*"; }

echo "waiting for control plane at $API"
for i in $(seq 1 60); do
  if curl -sf "$API/healthz" >/dev/null; then
    break
  fi
  sleep 2
  if [ "$i" = 60 ]; then
    fail "control plane not reachable"
  fi
done
ok "control plane health"

login() {
  curl -sf -X POST "$API/api/v1/login" \
    -H 'content-type: application/json' \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}"
}

echo "logging in as $ADMIN_EMAIL"
AUTH=""
for i in $(seq 1 30); do
  if AUTH="$(login)"; then
    break
  fi
  sleep 2
  if [ "$i" = 30 ]; then
    fail "admin login"
  fi
done
TOKEN="$(printf '%s' "$AUTH" | python3 -c 'import json,sys; print(json.load(sys.stdin)["session_token"])')"
test -n "$TOKEN" || fail "empty session"
ok "admin session"

auth() { curl -sf -H "authorization: Bearer $TOKEN" "$@"; }

KEY_JSON="$(auth -X POST "$API/api/v1/api-keys" -H 'content-type: application/json' -d '{"name":"e2e"}')"
API_KEY="$(printf '%s' "$KEY_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')"
test -n "$API_KEY" || fail "api key"
ok "api key ${API_KEY:0:12}"

mkdir -p "$ROOT/bin"
if [ ! -x "$ROOT/bin/app-plat" ]; then
  (cd "$ROOT" && CGO_ENABLED=0 go build -o bin/app-plat ./cmd/app-plat)
fi
"$ROOT/bin/app-plat" login --api-key "$API_KEY" --api-url "$API" >/dev/null
"$ROOT/bin/app-plat" apps list >/dev/null
ok "cli"

CAPS="$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"capabilities","arguments":{"query":"apps"}}}' \
  | "$ROOT/bin/app-plat" mcp)"
printf '%s' "$CAPS" | grep -q 'apps list' || fail "mcp capabilities: $CAPS"
ok "mcp capabilities"

upload_app() {
  local name="$1" dir="$2" slug
  slug="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]' | tr ' ' '-')"
  local archive="$ROOT/tmp/${slug}.tar.gz"
  mkdir -p "$ROOT/tmp"
  tar -C "$dir" -czf "$archive" .
  "$ROOT/bin/app-plat" apps create --name "$name" --port 8080 --persistence postgresql,blob,keyvalue >/dev/null
  "$ROOT/bin/app-plat" apps upload "$slug" "$archive" >/dev/null
  echo "$slug"
}

wait_running() {
  local slug="$1" url
  for i in $(seq 1 90); do
    local status
    status="$("$ROOT/bin/app-plat" apps get "$slug" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')"
    echo "  $slug status=$status"
    if [ "$status" = "failed" ]; then
      "$ROOT/bin/app-plat" apps get "$slug"
      fail "$slug failed"
    fi
    if [ "$status" = "running" ]; then
      url="$("$ROOT/bin/app-plat" apps get "$slug" | python3 -c 'import json,sys; print(json.load(sys.stdin)["public_url"])')"
      if curl -sf --max-time 10 "${url}healthz" | grep -q ok; then
        return 0
      fi
    fi
    sleep 5
  done
  fail "$slug did not become ready"
}

echo "deploying golang example"
GO_SLUG="$(upload_app "Golang Demo" "$ROOT/examples/golang-app")"
wait_running "$GO_SLUG"
GO_URL="$("$ROOT/bin/app-plat" apps get "$GO_SLUG" | python3 -c 'import json,sys; print(json.load(sys.stdin)["public_url"])')"
curl -sf "${GO_URL}healthz" | grep -q ok || fail "golang healthz"
ok "golang app $GO_URL"

echo "deploying python example"
PY_SLUG="$(upload_app "Python Demo" "$ROOT/examples/python-app")"
wait_running "$PY_SLUG"
PY_URL="$("$ROOT/bin/app-plat" apps get "$PY_SLUG" | python3 -c 'import json,sys; print(json.load(sys.stdin)["public_url"])')"
curl -sf "${PY_URL}healthz" | grep -q ok || fail "python healthz"
ok "python app $PY_URL"

echo "checking persistence pages"
curl -sf "$GO_URL" | grep -qi 'postgres' || fail "golang page missing postgres"
ok "golang persistence page"

echo "checking default deny from a user app to the control plane"
if kubectl -n "$NS" exec "deploy/app-${PY_SLUG}" -c app -- python -c 'import urllib.request,sys
try:
    urllib.request.urlopen("http://control-plane:8080/healthz", timeout=5)
except Exception:
    sys.exit(1)
sys.exit(0)'; then
  fail "user app reached control plane"
fi
ok "user app cannot reach control plane"

echo "llm catalog"
auth -X POST "$API/api/v1/admin/llm/models/sync" -H 'content-type: application/json' -d '{}' >/dev/null || echo "warn: model sync failed"
MODEL="$(auth "$API/api/v1/admin/llm/models" | python3 -c 'import json,sys; ms=json.load(sys.stdin); print(next((m["id"] for m in ms if "gpt-4o-mini" in m["id"]), ms[0]["id"] if ms else ""))')"
if [ -n "$MODEL" ]; then
  auth -X PUT "$API/api/v1/admin/llm/models" -H 'content-type: application/json' -d "{\"models\":[{\"id\":\"$MODEL\",\"enabled\":true}]}" >/dev/null
  APP_ID="$("$ROOT/bin/app-plat" apps get "$GO_SLUG" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
  auth -X POST "$API/api/v1/llm/requests" -H 'content-type: application/json' \
    -d "{\"app_id\":\"$APP_ID\",\"models\":[\"$MODEL\"],\"spending_cap_usd\":1}" >/dev/null
  ok "llm grant requested for $MODEL"
else
  echo "warn: no models synced; skipping llm call"
fi

echo "component health"
auth "$API/api/v1/admin/health"
ok "admin health"

echo
echo "e2e passed"
echo "admin: $ADMIN_EMAIL / $ADMIN_PASSWORD"
echo "user:  http://app.127.0.0.1.nip.io:30080/"
echo "admin: http://admin.127.0.0.1.nip.io:30080/"
echo "api:   $API"
