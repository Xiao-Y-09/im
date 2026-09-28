#!/usr/bin/env bash
# Phase 1 冒烟：注册两个用户 → 登录 → 建群 → 第二人加群 → 成员有 2 人；外加 401 / 403 / 分层检查。
# 用法（在 server/ 下，服务已启动）：bash scripts/phase1_smoke.sh
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(cd .. && pwd)
source scripts/lib.sh

API=${API:-http://localhost:8080}
S=$(date +%s)

code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/api/register" \
  -H 'Content-Type: application/json' -d "{\"username\":\"alice_$S\",\"password\":\"secret123\"}")
check "register alice → 201" 201 "$code"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/api/register" \
  -H 'Content-Type: application/json' -d "{\"username\":\"bob_$S\",\"password\":\"secret123\"}")
check "register bob → 201" 201 "$code"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/api/register" \
  -H 'Content-Type: application/json' -d "{\"username\":\"alice_$S\",\"password\":\"secret123\"}")
check "duplicate username → 409" 409 "$code"

read -r TA UA <<<"$(register_login "$API" "alice_$S")"
read -r TB UB <<<"$(register_login "$API" "bob_$S")"
read -r TC UC <<<"$(register_login "$API" "carol_$S")"
check "alice got token" yes "$([ -n "$TA" ] && echo yes || echo no)"
check "bob got token" yes "$([ -n "$TB" ] && echo yes || echo no)"

me=$(curl -sS "$API/api/me" -H "Authorization: Bearer $TA" | json_get username)
check "/api/me returns alice" "alice_$S" "$me"

GID=$(api_post "$API" /api/groups "{\"name\":\"g_$S\"}" "$TA" | json_get id)
check "create group returns id" yes "$([ -n "$GID" ] && echo yes || echo no)"

api_post "$API" "/api/groups/$GID/join" '{}' "$TB" >/dev/null
members=$(curl -sS "$API/api/groups/$GID/members" -H "Authorization: Bearer $TA")
n=$(echo "$members" | grep -o '"username"' | wc -l | tr -d ' ')
check "group has 2 members" 2 "$n"

mygroups=$(curl -sS "$API/api/my/groups" -H "Authorization: Bearer $TB")
check_grep "bob's /api/my/groups contains the group" "\"id\":$GID" <(echo "$mygroups")

found=$(curl -sS "$API/api/users?q=bob_$S" -H "Authorization: Bearer $TA" | json_get username)
check "user search finds bob" "bob_$S" "$found"

code=$(curl -sS -o /dev/null -w '%{http_code}' "$API/api/my/groups")
check "no token → 401" 401 "$code"

code=$(curl -sS -o /dev/null -w '%{http_code}' "$API/api/messages?conv_id=g_$GID" -H "Authorization: Bearer $TC")
check "non-member history → 403" 403 "$code"

code=$(curl -sS -o /dev/null -w '%{http_code}' "$API/api/messages?conv_id=g_$GID" -H "Authorization: Bearer $TB")
check "member history → 200" 200 "$code"

layer=$(grep -rn "gorm.DB\|db\." internal/api internal/service || true)
check "no db access in api/service" "" "$layer"

summary
