# Phase 4 — Kafka 分布式 Transport + Redis 在线状态

## 目标
`mode: kafka` 下起两个实例（A: 8080，B: 8081），连在不同实例的用户能互相聊天，所有人看到的消息顺序完全一致；Redis 提供跨实例在线状态。

## 前置
Phase 3 已通过；`im-messages` topic 为 1 partition。

## 任务

1. `internal/transport/kafka/kafka.go`
   - Writer：

     ```go
     &kafka.Writer{
         Addr:         kafka.TCP(cfg.Brokers...),
         Topic:        cfg.Topic,
         RequiredAcks: kafka.RequireAll,
         BatchTimeout: 5 * time.Millisecond, // 默认 1s，不改会导致每条消息延迟约 1 秒
         BatchSize:    500,
     }
     ```

     `Publish`：`WriteMessages(ctx, kafka.Message{Key: []byte(env.ConvID), Value: json})`；错误原样返回（service 层回给发送者 `code:"unavailable"`）。
   - `Subscribe` 对每个订阅建一个 Reader：
     - `Broadcast: true` → `GroupID = "im-" + sub.Name + "-" + instanceID`
     - `Broadcast: false` → `GroupID = "im-" + sub.Name`（所有实例共享）
     - `StartOffset: kafka.LastOffset`（新 group 不重放历史；历史走 MySQL）
   - `Start`：每个 Reader 一个 goroutine：`FetchMessage` → 反序列化 → `env.Seq = m.Offset + 1` → Handler → `CommitMessages`。
   - `Close`：关 Writer 和所有 Reader。

2. `main.go` 的 switch 加上 `case "kafka": t = kafka.New(cfg.Transport.Kafka, cfg.InstanceID)`。其余代码不改——这是本 phase 的核心检查点。

3. `internal/presence/presence.go`（Redis）
   - `Online(uid)`：`SET online:{uid} {instanceID} EX 90`
   - `Refresh(uid)`：`EXPIRE online:{uid} 90`（writePump 每次发 ping 时调用）
   - `Offline(uid)`：该用户在本实例的最后一个连接断开时 `DEL`（用 Lua 或 `GET` 比对 instanceID 后再删，避免删掉另一实例刚写的值）
   - `Batch(uids) map[int64]bool`：`MGET`
   - Hub 在 Register / Unregister 时调用；local 模式也使用 Redis。

4. `GET /api/presence?uids=1,2,3` → `{"1":true,"2":false,...}`（api → service → presence，presence 在这里扮演 dao 的角色）。

5. `scripts/order_check/main.go`：3 个用户分别连 8080、8081、8080，加入同一个群，同时各发 300 条；每个客户端记录收到的 seq 列表；最后比较三份列表完全相同、严格递增、长度 900。

## 不要做
- 不改 Transport interface、Hub、MessageService（如果不得不改，停下问用户）。
- 不给 topic 加 partition。

## 验收
- [ ] 两个实例：用户 1 连 8080、用户 2 连 8081，单聊和群聊互通
- [ ] `order_check` 通过（三份 seq 列表一致）
- [ ] MySQL `SELECT COUNT(*), COUNT(DISTINCT client_msg_id) FROM messages` 两数相等（无重复落库）
- [ ] 找到当前持有 `im-persist` 的实例（`kafka-consumer-groups.sh --describe --group im-persist`），kill 它 → 继续发消息 → 另一实例接手落库，消息不丢
- [ ] `/api/presence` 能看到连在另一个实例上的用户在线；该用户断开 90s 内变离线
- [ ] 改回 `mode: local`、只起一个实例，不改任何代码就能运行（切换前清空 messages 表，并在 PROGRESS.md 注明原因：两种模式的 seq 来源不同）
- [ ] `go vet ./... && go build ./...` 通过；`-race` 下跑过一次两实例测试

完成后更新 `PROGRESS.md`，停下等用户确认。
