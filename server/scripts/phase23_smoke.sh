#!/usr/bin/env bash
# Phase 2/3 冒烟：单聊（含发送者回显）、三人群聊、非群成员收到 error、seq 递增。
# 用法（在 server/ 下，服务已启动）：bash scripts/phase23_smoke.sh
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(cd .. && pwd)
source scripts/lib.sh

API=${API:-http://localhost:8080}
S=$(date +%s)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

WSC="$TMP/wsclient$(go env GOEXE)"
go build -o "$WSC" ./scripts/wsclient

read -r TA UA <<<"$(register_login "$API" "p2a_$S")"
read -r TB UB <<<"$(register_login "$API" "p2b_$S")"
read -r TC UC <<<"$(register_login "$API" "p2c_$S")"
read -r TD UD <<<"$(register_login "$API" "p2d_$S")"
GID=$(api_post "$API" /api/groups "{\"name\":\"p2_$S\"}" "$TA" | json_get id)
api_post "$API" "/api/groups/$GID/join" '{}' "$TB" >/dev/null
api_post "$API" "/api/groups/$GID/join" '{}' "$TC" >/dev/null
echo "a=$UA b=$UB c=$UC d=$UD(non-member) group=$GID"

printf '/sleep 4000\n' | "$WSC" -api "$API" -user "p2b_$S" -linger 1s >"$TMP/b.log" 2>&1 &
B=$!
printf '/sleep 4000\n' | "$WSC" -api "$API" -user "p2c_$S" -linger 1s >"$TMP/c.log" 2>&1 &
C=$!
printf '/sleep 1500\n/g %s hello-from-d\n/sleep 1000\n' "$GID" |
  "$WSC" -api "$API" -user "p2d_$S" -linger 1s >"$TMP/d.log" 2>&1 &
D=$!
printf '/sleep 1500\n/to %s private-hi\n/g %s group-1\n/g %s group-2\n/g %s group-3\n/sleep 1500\n' "$UB" "$GID" "$GID" "$GID" |
  "$WSC" -api "$API" -user "p2a_$S" -linger 1s >"$TMP/a.log" 2>&1
wait "$B" "$C" "$D" || true

check_grep "b received private chat" '<< .*"type":"chat".*private-hi' "$TMP/b.log"
check_grep "a received own echo (send ack)" '<< .*"type":"chat".*private-hi' "$TMP/a.log"
for who in a b c; do
  n=$(grep -c '<< .*"conv_id":"g_'"$GID"'".*group-' "$TMP/$who.log" || true)
  check "$who received 3 group messages" 3 "$n"
done
check_grep "non-member d got error" '<< .*"type":"error".*not a member' "$TMP/d.log"
bad=$(grep -c "hello-from-d" "$TMP/b.log" || true)
check "non-member message not delivered" 0 "$bad"

seqs=$(grep '<< .*"type":"chat"' "$TMP/a.log" | sed -n 's/.*"seq":\([0-9]*\).*/\1/p' | tr '\n' ' ')
sorted=$(echo "$seqs" | tr ' ' '\n' | grep . | sort -n -u | tr '\n' ' ')
check "seq strictly increasing ($seqs)" "$sorted" "$seqs"

summary
