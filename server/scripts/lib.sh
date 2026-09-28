# 冒烟脚本共用的小工具。用 sed 解析 JSON，免得依赖 jq（Windows 的 Git Bash 默认没有）。
# 注意不要全局设置 MSYS_NO_PATHCONV：那样 Windows 版 curl 会收到字面量 /dev/null。

PASS=0
FAIL=0

# json_get <key>：从 stdin 的 JSON 里取最后一个同名字段（字符串或数字）
json_get() {
  sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\).*/\1/p" | head -1
}

check() { # check <名字> <期望> <实际>
  if [ "$2" = "$3" ]; then
    echo "PASS  $1"
    PASS=$((PASS + 1))
  else
    echo "FAIL  $1 (expected '$2', got '$3')"
    FAIL=$((FAIL + 1))
  fi
}

check_grep() { # check_grep <名字> <正则> <文件>
  if grep -q -- "$2" "$3"; then
    echo "PASS  $1"
    PASS=$((PASS + 1))
  else
    echo "FAIL  $1 (pattern '$2' not in $3)"
    FAIL=$((FAIL + 1))
  fi
}

summary() {
  echo "----"
  echo "passed=$PASS failed=$FAIL"
  [ "$FAIL" -eq 0 ]
}

# api_post <base> <path> <json> [token]
api_post() {
  if [ -n "${4:-}" ]; then
    curl -sS -X POST "$1$2" -H 'Content-Type: application/json' -H "Authorization: Bearer $4" -d "$3"
  else
    curl -sS -X POST "$1$2" -H 'Content-Type: application/json' -d "$3"
  fi
}

# register_login <base> <username> → 输出 "token uid"
register_login() {
  api_post "$1" /api/register "{\"username\":\"$2\",\"password\":\"secret123\"}" >/dev/null
  local resp
  resp=$(api_post "$1" /api/login "{\"username\":\"$2\",\"password\":\"secret123\"}")
  echo "$(echo "$resp" | json_get token) $(echo "$resp" | json_get id)"
}

# mysql_query <sql>：在 compose 的 mysql 容器里执行，输出不带表头
mysql_query() {
  docker compose -f "$ROOT/deploy/docker-compose.yml" exec -T mysql \
    mysql -uroot -proot im -N -e "$1" 2>/dev/null
}
