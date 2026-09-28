# Phase 3 — Transport 接口 + 单机 channel 背压 + 持久化

## 目标
消息先进 Transport 再投递；实现 `local` Transport（有界 Go channel + backpressure）；persister 批量落库；expvar 指标。

## 前置
Phase 2 已通过。

## 任务

1. `internal/transport/transport.go`

   ```go
   var ErrBusy = errors.New("transport busy")

   type Handler func(env *model.Envelope)

   type Subscription struct {
       Name      string // "deliver" | "persist"
       Broadcast bool   // true: 每个实例各收一份；false: 所有实例合起来只收一份
   }

   type Transport interface {
       Publish(ctx context.Context, env *model.Envelope) error
       Subscribe(ctx context.Context, sub Subscription, h Handler) error // 必须在 Start 前调用
       Start(ctx context.Context) error
       Close() error
   }
   ```

2. `internal/transport/local/local.go`
   - `ch chan *model.Envelope`，容量 = `buffer_size`。
   - `Publish`：`select { case ch <- env: ; case <-time.After(timeout): return ErrBusy; case <-ctx.Done(): return ctx.Err() }`。用可复用的 `time.Timer`，不要每次 `time.After` 泄漏。
   - `Start`：起**唯一一个** dispatcher goroutine：出队 → `seq++` → `env.Seq = seq` → 依次调用所有 Handler。
   - 构造时传入初始 seq（`MessageDAO.MaxSeq()`）。
   - Handler 不能阻塞 dispatcher：Hub.Deliver 本身非阻塞；persister.Enqueue 见下。

3. `internal/persist/persister.go`
   - 自己的有界 channel（容量 8192）。`Enqueue(env)`：`call_*` 直接忽略；channel 满 → 计数 `persist_dropped` 并 log（不阻塞 dispatcher）。
   - 后台 goroutine：攒到 200 条或每 50ms flush 一次，调 `MessageDAO.BatchInsertIgnore`；失败重试 3 次后 log。
   - `Close` 时把剩余的 flush 完。

4. `internal/metrics`：`expvar.NewInt`：`published`、`busy_rejected`、`delivered`、`slow_kicked`、`persisted`、`persist_dropped`。在对应位置 `Add(1)`。gin 挂 `GET /debug/vars` → `expvar.Handler()`。

5. 改 `MessageService.Send`：删除 Phase 2 的临时 seq 和直接 Deliver，改为 `transport.Publish`；`ErrBusy` → 给发送者回 `{type:"error", payload:{code:"busy"}}`。

6. `main.go`：

   ```go
   var t transport.Transport
   switch cfg.Transport.Mode {
   case "local": t = local.New(cfg.Transport.Local, maxSeq)
   case "kafka": log.Fatal("kafka transport: Phase 4")
   default:      log.Fatalf("unknown transport mode %q", cfg.Transport.Mode)
   }
   t.Subscribe(ctx, transport.Subscription{Name: "deliver", Broadcast: true}, hub.Deliver)
   t.Subscribe(ctx, transport.Subscription{Name: "persist", Broadcast: false}, persister.Enqueue)
   t.Start(ctx)
   ```

   收到 SIGINT/SIGTERM 时优雅退出：停 HTTP → `t.Close()` → `persister.Close()`。

7. 单元测试 `local_test.go`：
   - 并发 100 个 goroutine 各 Publish 100 条 → Handler 收到 10000 条，seq 连续且严格递增。
   - buffer=1、timeout=1ms、Handler 故意 sleep → Publish 返回 `ErrBusy`。

## 不要做
- 不写 kafka 实现（Phase 4）。
- 不改 Hub 的投递规则。

## 验收
- [ ] `go test -race ./internal/transport/...` 通过
- [ ] wsclient 单聊/群聊仍然正常，消息带递增 seq
- [ ] 重启服务后 `GET /api/messages` 能拉到重启前的消息；新消息 seq 接着之前的最大值往上涨
- [ ] 临时改 config：`buffer_size: 1`、`publish_timeout_ms: 1`，写一个脚本狂发 → 看到 busy 错误，`/debug/vars` 里 busy_rejected > 0，进程内存不持续上涨
- [ ] `/debug/vars` 能看到全部 6 个计数器
- [ ] `go vet ./... && go build ./...` 通过

完成后更新 `PROGRESS.md`，停下等用户确认。
