# Phase 0 — 环境与骨架

## 目标
一条命令起 MySQL / Redis / Kafka；Go 服务能读 config 并回 `pong`。

## 前置
无。创建 `PROGRESS.md`，写入 Phase 0 标题。

## 任务

1. 建目录：`deploy/ server/ web/ loadtest/`。
2. `server/`：`go mod init <模块路径>`（先问用户模块路径，没回答就用 `im`）。安装锁定决策里的全部后端依赖。
3. 写 `deploy/docker-compose.yml`：

   ```yaml
   services:
     mysql:
       image: mysql:8.0
       environment: { MYSQL_ROOT_PASSWORD: root, MYSQL_DATABASE: im }
       ports: ["3306:3306"]
     redis:
       image: redis:7
       ports: ["6379:6379"]
     kafka:
       image: apache/kafka:3.8.0
       container_name: kafka
       ports: ["9092:9092"]
   ```

4. 写 `deploy/init-topic.sh`：循环等待 Kafka 可用，然后创建 topic（已存在就跳过）：

   ```bash
   docker exec kafka /opt/kafka/bin/kafka-topics.sh --create --if-not-exists \
     --topic im-messages --partitions 1 --replication-factor 1 \
     --bootstrap-server localhost:9092
   ```

5. 写 `server/config.yaml`：

   ```yaml
   instance_id: A
   http_addr: :8080
   mysql_dsn: root:root@tcp(localhost:3306)/im?parseTime=true&loc=Local
   redis_addr: localhost:6379
   jwt_secret: dev-secret
   upload_dir: ./uploads
   transport:
     mode: local            # local | kafka
     local:
       buffer_size: 4096
       publish_timeout_ms: 50
     kafka:
       brokers: [localhost:9092]
       topic: im-messages
   ```

6. `internal/config`：读 yaml；支持 flag 覆盖 `-config`、`-id`（instance_id）、`-addr`（http_addr）、`-mode`（transport.mode）。
7. `cmd/server/main.go`：加载 config，起 gin，注册 `GET /ping` → `pong`；启动日志打印 instance_id、addr、mode。

## 不要做
- 不连数据库、不写业务代码（Phase 1 才做）。

## 验收
- [ ] `docker compose -f deploy/docker-compose.yml up -d` 后 `docker compose ps` 三个容器都是 running
- [ ] `bash deploy/init-topic.sh` 后 `kafka-topics.sh --describe --topic im-messages` 显示 `PartitionCount: 1`
- [ ] `go run ./cmd/server` 后 `curl localhost:8080/ping` 返回 `pong`
- [ ] `go run ./cmd/server -id B -addr :8081` 能同时起第二个进程，日志显示 instance=B
- [ ] `go vet ./... && go build ./...` 通过

完成后更新 `PROGRESS.md`，停下等用户确认。
