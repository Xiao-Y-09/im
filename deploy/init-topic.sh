#!/usr/bin/env bash
# 等 Kafka 可用后创建 im-messages（1 个 partition，全局有序的来源）。已存在则跳过。
set -euo pipefail
# Git Bash 会把 /opt/... 改写成 Windows 路径，传给 docker exec 前必须关掉
export MSYS_NO_PATHCONV=1

KT=/opt/kafka/bin/kafka-topics.sh

for i in $(seq 1 60); do
  if docker exec kafka "$KT" --bootstrap-server localhost:9092 --list >/dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "kafka not ready after 120s" >&2
    exit 1
  fi
  echo "waiting for kafka ($i)..."
  sleep 2
done

docker exec kafka "$KT" --create --if-not-exists \
  --topic im-messages --partitions 1 --replication-factor 1 \
  --bootstrap-server localhost:9092

docker exec kafka "$KT" --describe --topic im-messages --bootstrap-server localhost:9092
