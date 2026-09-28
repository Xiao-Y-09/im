#!/usr/bin/env bash
# 依次跑完 Phase 7 的测试矩阵，结果行追加到 loadtest/results.md，完整输出在 loadtest/logs/。
#
# 前置：docker compose 已启动、topic 已建、已跑过 seed（go run ./loadtest seed ...）。
# 用法（在仓库根目录）：bash loadtest/run_matrix.sh
#   SKIP_CONN=1   跳过 5 分钟的连接数测试
#   RATES="2000 4000"  只跑部分吞吐档位
set -euo pipefail
cd "$(dirname "$0")/.."

ulimit -n 65535 2>/dev/null || echo "warn: could not raise ulimit -n (ok on Windows)"

RES=loadtest/results.md
LOGS=loadtest/logs
BIN=loadtest/bin
RATES=${RATES:-"2000 4000 6000 8000 10000 12000"}
COMPOSE="docker compose -f deploy/docker-compose.yml"
mkdir -p "$LOGS" "$BIN"

EXE=$(go env GOEXE)
SERVER="$BIN/server$EXE"
LT="$BIN/loadtest$EXE"
(cd server && go build -o "../$SERVER" ./cmd/server)
go build -o "$LT" ./loadtest

if [ ! -f "$RES" ]; then
  cat >"$RES" <<'EOF'
# 压测结果

| 模式 | 实例数 | 连接 | 群大小 | 目标 msg/s | 实际 msg/s | loss | p99 延迟 | busy 数 |
|---|---|---|---|---|---|---|---|---|
EOF
fi

{
  echo "# run at $(date '+%F %T')"
  echo "go: $(go version)"
  echo "os: $(uname -a)"
} >>"$LOGS/machine.txt"

PIDS=()

start_server() { # <id> <addr> <mode>
  "$SERVER" -config server/config.yaml -id "$1" -addr "$2" -mode "$3" >"$LOGS/server-$1-$3.log" 2>&1 &
  PIDS+=($!)
  local port=${2#:}
  for _ in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:$port/ping" >/dev/null 2>&1; then return 0; fi
    sleep 0.5
  done
  echo "server $1 did not become ready, see $LOGS/server-$1-$3.log" >&2
  exit 1
}

stop_servers() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done
  for p in "${PIDS[@]}"; do wait "$p" 2>/dev/null || true; done
  PIDS=()
}
trap stop_servers EXIT

clear_messages() {
  $COMPOSE exec -T mysql mysql -uroot -proot im -e "TRUNCATE TABLE messages" 2>/dev/null
}

rss_of() { # 仅在有 ps -o 的系统上可用（Linux/macOS）；Windows 上看 loadtest 输出的 sys_mb
  ps -o rss= -p "$1" 2>/dev/null | tr -d ' ' || echo "n/a"
}

run_lt() { # <logname> <label> <args...>
  local name=$1 label=$2
  shift 2
  echo "=== $name ==="
  "$LT" run -label "$label" -results-md "$RES" "$@" 2>&1 | tee "$LOGS/$name.txt"
}

kafka_lag() {
  # Git Bash 会把 /opt/... 改写成 Windows 路径，只对这条命令关掉路径转换
  MSYS_NO_PATHCONV=1 docker exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
    --describe --all-groups 2>/dev/null || true
}

# 1. 连接数测试：local 单实例，1000 连接保持 5 分钟
if [ "${SKIP_CONN:-0}" != "1" ]; then
  clear_messages
  start_server A :8080 local
  ( sleep 150; echo "server RSS at t=150s: $(rss_of "${PIDS[0]}") KB" >>"$LOGS/conn-test-rss.txt" ) &
  SAMPLER=$!
  run_lt conn-test local \
    -urls ws://127.0.0.1:8080/ws -conns 1000 -rate 100 -duration 300s -warmup 10s
  wait "$SAMPLER" || true
  stop_servers
  sleep 10
fi

# 2. 吞吐测试：kafka 两实例
start_server A :8080 kafka
start_server B :8081 kafka
for r in $RATES; do
  clear_messages
  sleep 10
  run_lt "kafka-$r" kafka \
    -urls ws://127.0.0.1:8080/ws,ws://127.0.0.1:8081/ws -conns 1000 -rate "$r" -duration 60s -warmup 10s
  kafka_lag >"$LOGS/kafka-$r-lag.txt"
done
stop_servers
sleep 10

# 3. 对照组：local 单实例 8000
clear_messages
start_server A :8080 local
run_lt local-8000 local \
  -urls ws://127.0.0.1:8080/ws -conns 1000 -rate 8000 -duration 60s -warmup 10s
stop_servers

echo "done → $RES"
