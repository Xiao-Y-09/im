# Phase 2 — WebSocket 网关

## 目标
浏览器通过 WebSocket 实时单聊、群聊。本 phase 还没有 Transport：`MessageService` 直接调用 `hub.Deliver`，seq 暂时用 Hub 内的内存计数器（Phase 3 会替换，且本 phase 不落库）。

## 前置
Phase 1 已通过。

## 任务

1. `internal/ws/client.go`

   ```go
   type Client struct {
       UserID int64
       conn   *websocket.Conn
       send   chan []byte // 容量 256
       hub    *Hub
   }
   ```

   - `readPump`：`SetReadLimit(64KB)`；`SetReadDeadline(now+60s)`，`SetPongHandler` 里续期；循环 `ReadMessage` → 解析 Envelope → 交给 `onMessage` 回调（由 api 层注入，最终调 `MessageService.Send`）。任何读错误 → `hub.Unregister(c)` 并退出。
   - `writePump`：`for` + `select`：从 `send` 取消息写出（`SetWriteDeadline(now+10s)`）；`time.Ticker` 每 30s 发 ping。`send` 被关闭或写错误 → 关连接退出。
   - 只有 writePump 调 `conn.Write*`，只有 readPump 调 `conn.Read*`。

2. `internal/ws/hub.go`

   ```go
   type Hub struct {
       mu      sync.RWMutex
       clients map[int64]map[*Client]struct{} // 一个用户可以多个 tab
   }
   func (h *Hub) Register(c *Client)
   func (h *Hub) Unregister(c *Client)        // 幂等；关闭 c.send
   func (h *Hub) Deliver(env *model.Envelope) // 见下
   func (h *Hub) trySend(c *Client, b []byte) // 非阻塞写 send；满了 → Unregister(c)（慢客户端被踢）
   ```

   `Deliver` 规则：
   - `call_*`：只发给 `env.To`。
   - 单聊 `u_a_b`：发给 a 和 b。
   - 群聊 `g_x`：发给群成员。群成员从 `GroupService` 取，Hub 内做内存缓存；`GroupService.Join` 成功后调用 `hub.InvalidateGroup(x)`（通过接口注入，不让 service 直接依赖 ws 包的具体类型）。
   - 只投递给连在本进程上的用户，不在线就跳过。
   - 发送者自己的所有 tab 也要收到（这就是发送确认）。

3. `internal/service/message.go`：`Send(uid, env)`：
   - 覆盖 `env.From = uid`、`env.TS = now`；校验 `type` 在白名单、`client_msg_id` 非空、payload ≤ 16KB。
   - `chat` / `file` 需要 `ConvService.CanAccess`；`call_*` 需要 `To` 非 0。
   - 校验失败 → 给发送者回 `{type:"error", client_msg_id, payload:{code, message}}`。
   - 通过 → 临时 seq（内存 atomic 计数）→ `hub.Deliver`。

4. `internal/api/ws.go`：`GET /ws?token=`，JWT middleware 验证后 `Upgrade`（`CheckOrigin` 开发阶段放行，写 TODO），创建 Client → Register → `go writePump()` → `go readPump()`。

5. `GET /debug/goroutines` 返回 `{"n": runtime.NumGoroutine()}`。

6. `scripts/wsclient/main.go`：一个命令行 WS 小客户端（登录 → 连接 → 读 stdin 发消息 → 打印收到的消息），用来测试。

## 不要做
- 不写 Transport、不落库、不写前端。

## 验收
- [ ] 两个终端各跑一个 wsclient（不同用户），单聊双方实时收到，发送者也收到回显
- [ ] 三个用户群聊，三方都收到
- [ ] 非群成员发群消息收到 `type:"error"`
- [ ] 连 50 个客户端后 `/debug/goroutines` 约增加 100；全部断开后回落到初始值附近
- [ ] `go run -race ./cmd/server` 下完成以上测试，无 DATA RACE 输出
- [ ] `go vet ./... && go build ./...` 通过

完成后更新 `PROGRESS.md`，停下等用户确认。
