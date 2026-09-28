#!/usr/bin/env bash
# Phase 5 冒烟：上传/下载 md5、413、伪造 url 被拒、file 消息投递、通话信令往返、呼叫离线用户、call_* 不落库。
# 用法（在 server/ 下）：
#   单实例：bash scripts/phase5_smoke.sh
#   跨实例：API_A=http://localhost:8080 API_B=http://localhost:8081 bash scripts/phase5_smoke.sh
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(cd .. && pwd)
source scripts/lib.sh

API_A=${API_A:-http://localhost:8080}
API_B=${API_B:-$API_A}
S=$(date +%s)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

md5() { md5sum "$1" | cut -d' ' -f1; }

echo "building wsclient..."
WSC="$TMP/wsclient$(go env GOEXE)"
go build -o "$WSC" ./scripts/wsclient

read -r TA UA <<<"$(register_login "$API_A" "p5a_$S")"
read -r TB UB <<<"$(register_login "$API_A" "p5b_$S")"
read -r TC UC <<<"$(register_login "$API_A" "p5c_$S")"
CONV="u_$((UA < UB ? UA : UB))_$((UA < UB ? UB : UA))"
echo "alice=$UA (on $API_A) bob=$UB (on $API_B) carol=$UC (never online) conv=$CONV"

# ---- 上传 / 下载 ----
{ printf '\x89PNG\r\n\x1a\n'; head -c $((5 * 1024 * 1024 - 8)) /dev/urandom; } >"$TMP/img.png"
up=$(curl -sS -F "file=@$TMP/img.png" "$API_A/api/upload" -H "Authorization: Bearer $TA")
URL=$(echo "$up" | json_get url)
MIME=$(echo "$up" | json_get mime)
SIZE=$(echo "$up" | json_get size)
check "5MB image upload returns /files/ url" yes "$(case "$URL" in /files/*) echo yes;; *) echo no;; esac)"
check "mime detected as image/png" image/png "$MIME"
curl -sS -o "$TMP/down.png" "$API_B$URL"
check "download md5 matches" "$(md5 "$TMP/img.png")" "$(md5 "$TMP/down.png")"

head -c $((21 * 1024 * 1024)) /dev/urandom >"$TMP/big.bin"
code=$(curl -sS -o /dev/null -w '%{http_code}' -F "file=@$TMP/big.bin" "$API_A/api/upload" -H "Authorization: Bearer $TA")
check "21MB upload → 413" 413 "$code"

# ---- file 消息 + 通话信令 ----
# bob 先上线；两边用 /sleep 控制时序，输出分别写日志后 grep
printf '/sleep 2500\n/call accept %s {"call_id":"c1"}\n/sleep 2500\n/call answer %s {"call_id":"c1","sdp":"fake-answer"}\n/sleep 2000\n' "$UA" "$UA" |
  "$WSC" -api "$API_B" -user "p5b_$S" -linger 1s >"$TMP/bob.log" 2>&1 &
BOB=$!

{
  printf '/sleep 1000\n'
  printf '/file %s %s img.png %s %s\n' "$CONV" "$URL" "$SIZE" "$MIME"
  printf '/file %s http://evil.com/x evil.png 1 image/png\n' "$CONV"
  printf '/call invite %s {"call_id":"c1"}\n' "$UB"
  printf '/sleep 3000\n'
  printf '/call offer %s {"call_id":"c1","sdp":"fake-offer"}\n' "$UB"
  printf '/sleep 2500\n'
  printf '/call hangup %s {"call_id":"c1"}\n' "$UB"
  printf '/call invite %s {"call_id":"c2"}\n' "$UC"
  printf '/sleep 1000\n'
} | "$WSC" -api "$API_A" -user "p5a_$S" -linger 1s >"$TMP/alice.log" 2>&1

wait "$BOB" || true

check_grep "bob received file message" "<< .*\"type\":\"file\".*$URL" "$TMP/bob.log"
check_grep "forged url rejected" '<< .*"type":"error".*file url must start with /files/' "$TMP/alice.log"
check_grep "bob received call_invite" '<< .*"type":"call_invite"' "$TMP/bob.log"
check_grep "alice received call_accept" '<< .*"type":"call_accept"' "$TMP/alice.log"
check_grep "bob received call_offer (sdp untouched)" '<< .*"type":"call_offer".*fake-offer' "$TMP/bob.log"
check_grep "alice received call_answer (sdp untouched)" '<< .*"type":"call_answer".*fake-answer' "$TMP/alice.log"
check_grep "bob received call_hangup" '<< .*"type":"call_hangup"' "$TMP/bob.log"
check_grep "calling offline user → call_reject offline" '<< .*"type":"call_reject".*"reason":"offline"' "$TMP/alice.log"

sleep 1 # 等 persister flush
calls=$(mysql_query "SELECT COUNT(*) FROM messages WHERE type LIKE 'call\\_%'" | tr -d '\r ')
check "no call_* rows in messages" 0 "$calls"
files=$(mysql_query "SELECT COUNT(*) FROM messages WHERE conv_id='$CONV' AND type='file'" | tr -d '\r ')
check "file message persisted once" 1 "$files"

if [ "$FAIL" -ne 0 ]; then
  echo "---- alice.log"; cat "$TMP/alice.log"
  echo "---- bob.log"; cat "$TMP/bob.log"
fi
summary
