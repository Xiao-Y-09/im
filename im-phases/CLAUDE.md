# 分布式 IM 系统 — 项目总规则（Claude 必读）

## 你的工作方式

1. 每次只做**一个 phase**。用户会说“开始 Phase N”，你就读 `phases/phase-N-*.md` 并严格照做。
2. 开始前先确认上一个 phase 的验收已通过（看 `PROGRESS.md`）。没通过就停下来告诉用户。
3. 做完后逐条跑完该 phase 的「验收」，把结果（命令 + 输出摘要 + 通过/失败）写进 `PROGRESS.md`。
4. **验收全部通过后停下，等用户说“继续”再进入下一个 phase。** 不要顺手做下一个 phase 的内容。
5. 遇到文件里没写清楚、而且会影响后续 phase 的决定：停下来问用户，不要自己拍板。小的实现细节可以自己定，在 `PROGRESS.md` 里记一行。
6. 不要改「锁定决策」里的任何一条。觉得有问题就提出来，等用户决定。

## 项目目标

一个 Go + Vue 的即时通讯系统，支持单聊、群聊、文件传输、音视频通话信令。
同一套代码可以单机运行（Go channel 做消息传输），也可以多实例运行（Kafka 做消息传输），靠 `config.yaml` 一行切换。

## 锁定决策（不许改）

| 项 | 决定 |
|---|---|
| 后端 | Go 1.22+，gin，gorm（MySQL driver），gorilla/websocket，segmentio/kafka-go，redis/go-redis/v9，golang-jwt/jwt/v5，gopkg.in/yaml.v3 |
| 前端 | Vue 3 + Vite + Pinia + vue-router，JavaScript（不用 TS） |
| 基础设施 | docker compose：mysql:8.0、redis:7、apache/kafka:3.8.0（KRaft 单节点） |
| 分层 | `api`（只处理 HTTP/WS 收发）→ `service`（业务）→ `dao`（只写 SQL）。上层调下层，不跨层，不反向。api 和 service 里不能出现 `db.` 调用 |
| 连接模型 | 每个 WS 连接 2 个 goroutine：`readPump`（唯一的读者）和 `writePump`（唯一的写者）。所有要发给某人的数据都先进他的 `send chan []byte`（容量 256） |
| Transport | 一个 `Transport` interface，两个实现：`local`（有界 Go channel）和 `kafka`。业务代码只依赖 interface |
| Kafka topic | `im-messages`，**只有 1 个 partition**（全局有序的来源） |
| seq（消息顺序号） | local 模式：唯一的 dispatcher goroutine 出队时 `seq++`（启动时初始化为 `MAX(seq)`）。kafka 模式：`seq = offset + 1`。seq 在发送时**不**生成 |
| 持久化 | 单独的 persister 订阅消息，批量 `INSERT IGNORE` 写 MySQL；`call_*` 类型不落库 |
| Redis 用途 | 跨实例在线状态 `online:{uid}`（值为 instanceID，TTL 90s） |
| 文件 | 本体走 HTTP 上传（上限 20MB，存 `./uploads`），WS 只传文件消息（url 等元数据） |
| 通话 | WebRTC 点对点；服务器只转发信令，不碰音视频数据 |
| 发送确认 | 不做单独 ack：发送者自己也会收到一份回显，前端用 `client_msg_id` 对上 |

## 目录结构

```
im/
├── CLAUDE.md
├── PROGRESS.md
├── phases/
├── deploy/docker-compose.yml
├── server/
│   ├── go.mod
│   ├── config.yaml
│   ├── cmd/server/main.go
│   └── internal/
│       ├── config/     # 读 config.yaml + flag 覆盖
│       ├── model/      # gorm 模型 + Envelope
│       ├── dao/
│       ├── service/
│       ├── api/        # gin handler、JWT middleware、WS upgrade
│       ├── ws/         # Hub、Client、readPump/writePump
│       ├── transport/  # transport.go（interface）+ local/ + kafka/
│       ├── presence/   # Redis 在线状态
│       ├── persist/    # 批量写库
│       └── metrics/    # expvar 计数器
├── web/
└── loadtest/
```

## 统一消息格式 Envelope

```go
type Envelope struct {
    Type        string          `json:"type"`          // chat | file | call_invite | call_accept | call_reject | call_offer | call_answer | call_ice | call_hangup | error
    ConvID      string          `json:"conv_id"`       // 单聊 u_{小id}_{大id}，群聊 g_{groupId}
    From        int64           `json:"from"`          // 服务端根据 token 填，不信任客户端
    To          int64           `json:"to,omitempty"`  // 只有 call_* 用
    ClientMsgID string          `json:"client_msg_id"` // 前端生成的 uuid
    Seq         int64           `json:"seq"`           // 服务端填
    Payload     json.RawMessage `json:"payload"`
    TS          int64           `json:"ts"`            // 服务端填，毫秒
}
```

## 代码规范

- 每个 phase 结束时 `go vet ./...` 和 `go build ./...` 必须通过；有并发代码的 phase 要用 `-race` 跑过一次。
- 错误不吞：要么返回，要么 log 后给客户端回 `{type:"error"}`。
- 不引入锁定决策以外的依赖；确实需要时先问用户（例外：`golang.org/x/crypto/bcrypt`、`github.com/google/uuid`、`golang.org/x/time/rate` 可直接用）。
- 注释写“为什么”，不写“做了什么”。

## PROGRESS.md 格式

```
## Phase N — <名字>  状态：进行中 / 已通过
- 决定：<自己定的小细节，一行一个>
- 验收：
  - [x] <验收项> — <命令> → <结果摘要>
```
