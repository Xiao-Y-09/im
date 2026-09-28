# PROGRESS

> 用户要求“按顺序把 phases 里所有文件一次性写完”，所以 8 个 phase 连续完成，中间没有停下等确认。
> 下面每个 phase 的验收都是实际跑过的（命令 + 结果摘要）；需要人在浏览器里操作的项单独列出步骤。
> 测试环境：Windows 11 + Docker Desktop，Go 1.26.4，Node 24。服务端测试时用 `go build -race` 的二进制运行。

## 环境相关的全局决定（影响所有 phase）

- 决定：Go 模块路径用 `im`（phase-0 说先问用户，本次按“没回答就用 im”处理）。
- 决定：`go.mod` 为 `go 1.26.0`：最新 gin 要求更高的 go 版本，`go get` 自动升级；满足“Go 1.22+”。
- 决定：仓库根目录放 `go.work`（use ./server ./loadtest），这样 phase-7 的 `go run ./loadtest ...` 可以在根目录直接运行。
- 决定：`deploy/docker-compose.yml` 加了 `name: im`：本机另一个项目也在名为 `deploy` 的目录里，默认项目名相同会导致 `compose up/down` 误伤对方容器（已发现并只删除了自己刚创建的 3 个容器）。
- 决定：Redis 映射到宿主机 **6380**（6379 已被本机其他项目的 `pdf-redis` 占用），`config.yaml` 同步。
- 决定：所有连接地址用 **127.0.0.1** 而不是 `localhost`，Kafka 对外通告 `127.0.0.1:9092`（compose 里补全了 KRaft 单节点配置）。原因：Windows 上 `localhost` 先解析到 `::1`，经 Docker Desktop 的 IPv6 端口转发每次往返约 40ms——实测 MySQL 43.6ms/查询 vs 1.0ms，压测 2000 msg/s 时 dispatcher 被 DB 连接池排队饿死（loss 100%）。改后同样压测 loss 0。
- 决定：原始的 `CLAUDE.md` / `phases/` 仍在 `im-phases/` 下，没有移动；代码按 CLAUDE.md 的目录结构放在仓库根目录。

## Phase 0 — 环境与骨架  状态：已通过
- 决定：`deploy/init-topic.sh` 里设置 `MSYS_NO_PATHCONV=1`，否则 Git Bash 会把 `/opt/kafka/...` 改写成 Windows 路径。
- 决定：两个实例同时启动时 AutoMigrate 会并发建表报 "already exists"，main 里对迁移重试最多 3 次。
- 验收：
  - [x] 三个容器 running — `docker compose -f deploy/docker-compose.yml ps` → mysql/redis/kafka running
  - [x] topic 1 partition — `bash deploy/init-topic.sh` → `PartitionCount: 1`
  - [x] `/ping` — `curl localhost:8080/ping` → `pong`
  - [x] 第二个实例 — `server -id B -addr :8081` → 日志 `starting instance=B addr=:8081`，`curl :8081/ping` → `pong`
  - [x] `go vet ./... && go build ./...` → 无输出，通过

## Phase 1 — 分层后端 + MySQL  状态：已通过
- 决定：service 返回 `*service.Error{Code, Msg}`，api 层按 code 映射 HTTP 状态；统一错误体 `{"error","message"}`；内部错误只 log 不外泄。
- 决定：用户名 `^[A-Za-z0-9_]{3,32}$`，密码 6–72 位（bcrypt 上限 72 字节）；重名注册返回 409。
- 决定：`GET /api/groups/:id/members` 仅群成员可看（否则 403），返回 `[{id, username}]`。
- 决定：`GET /api/users` 除了 `?q=`（前缀搜索）还支持 `?ids=1,2`（批量取用户名）。前端展示群消息发送者/单聊对象名字需要它；是现有路由的扩展，不是新接口。
- 决定：gorm 开 `TranslateError`，dao 把重复键/未找到翻译成 `dao.ErrDuplicate/ErrNotFound`，上层不接触 gorm 错误类型。
- 决定：冒烟脚本用 sed 解析 JSON（Windows Git Bash 默认没有 jq），公共函数在 `server/scripts/lib.sh`。
- 验收：
  - [x] 冒烟脚本 — `bash scripts/phase1_smoke.sh` → 14/14 PASS（注册×2、重名 409、登录、/me、建群、加群、成员 2 人、my/groups、搜人）
  - [x] 无 token → 401 — 同上脚本 `no token → 401` PASS
  - [x] 非成员拉群历史 → 403 — 同上脚本 PASS
  - [x] `grep -rn "gorm.DB\|db\." internal/api internal/service` → 无结果（脚本内检查 PASS）
  - [x] `go vet ./... && go build ./...` 通过

## Phase 2 — WebSocket 网关  状态：已通过
- 决定：`MessageService.Send` 返回“只回给发送者”的 Envelope（错误或离线拒接），api 注入的 onMessage 通过 `Client.Reply` 发回；ws 包不依赖 service。
- 决定：Hub 锁约定：只在持写锁时 `close(c.send)`，只在持读锁时写 `c.send`，杜绝向已关闭 channel 发送。慢客户端先收集、释放读锁后再 Unregister。
- 决定：conv_id 解析函数放在 `model`（service 鉴权和 ws 投递共用）。
- 决定：群成员缓存除了 Join 时 `InvalidateGroup`，还有 5 秒 TTL：多实例时别的实例上的 Join 只能让那台的缓存失效；另用 generation 计数防止“查询中途被失效”写回旧数据。
- 决定：`client_msg_id` 最长 36（对应 CHAR(36)）；空 payload 规范成 `{}`。
- 决定：Unregister 可能在 dispatcher 里被调用（踢慢客户端），所以 Redis Offline 放到后台 goroutine，并在执行前复查用户是否已重连。
- 决定：Phase 2 的“临时内存 seq + 直接 Deliver”在一次性完成时被 Phase 3 取代，最终代码里不存在；投递规则、回显、错误回复与 Phase 2 描述一致。
- 验收：
  - [x] 单聊双方实时收到 + 发送者回显 — `bash scripts/phase23_smoke.sh` → `b received private chat` / `a received own echo` PASS
  - [x] 三人群聊三方都收到 — 同上 `a/b/c received 3 group messages` PASS
  - [x] 非群成员发群消息收到 error — 同上 `non-member d got error` + `not delivered` PASS
  - [x] 50 连接 goroutine — `go run ./scripts/flood -conns 50` → before=34, with_50_conns=134（+100，2.00/连接），关闭后 34
  - [x] `-race` — 以上全部在 `go build -race` 的服务端上完成，`grep -c "DATA RACE"` 服务端日志 → 0
  - [x] `go vet ./... && go build ./...` 通过

## Phase 3 — Transport + 单机背压 + 持久化  状态：已通过
- 决定：`Publish` 先走无定时器的快路径；满了才从 `sync.Pool` 取可复用 Timer，归还前 Stop 并清空 channel。
- 决定：新增 `transport.ErrClosed`；Close 后 dispatcher 把已接收的消息处理完再退出（它们的发送者已算成功）。
- 决定：退出顺序：停 HTTP → `hub.CloseAll()`（断开 WS、清在线状态）→ `t.Close()` → `persister.Close()`。
- 决定：persister 只存 `chat`/`file`（等价于“call_* 忽略”，error 本来就不会进 Transport）；队列满时丢弃计数，日志每秒最多一行。
- 决定：busy 测试的“狂发脚本”是 `scripts/flood`（N 个连接同群、每连接尽快发 n 条，统计 error 码和 /debug/vars 增量）。
- 验收：
  - [x] `go test -race ./internal/transport/...` → `ok im/internal/transport/local`（100×100 并发 seq 连续递增；buffer=1 + 慢 handler → ErrBusy；Close 后 ErrClosed）
  - [x] wsclient 单聊/群聊正常且 seq 递增 — `phase23_smoke.sh` → `seq strictly increasing (1 2 3 4)` PASS
  - [x] 重启后历史仍在、seq 接着涨 — 重启日志 `seq continues from 4`；发一条得 seq=5 → 重启 → `/api/messages` 返回 seq 5 → 新消息 seq=6
  - [x] busy 背压 — config 改 `buffer_size: 1, publish_timeout_ms: 1`，`flood -conns 50 -n 400` 两轮：busy 错误 80 / 146，`busy_rejected` +80 / +147，heap 3.3→5.8→7.0MB（不持续上涨），同时 slow_kicked=20（50 人群每条消息扇出 50 份，接收端跟不上被踢，符合设计）
  - [x] `/debug/vars` 有 6 个计数器 — published / busy_rejected / delivered / slow_kicked / persisted / persist_dropped 均出现
  - [x] `go vet ./... && go build ./...` 通过

## Phase 4 — Kafka Transport + Redis 在线状态  状态：已通过
- 决定：Reader 用 `CommitInterval: 1s` 批量提交 offset。同步逐条提交每条消息一次网络往返，会把吞吐压到约 1k/s；代价是崩溃时最多重放约 1 秒的消息，由 `INSERT IGNORE` 和前端按 seq/client_msg_id 去重兜住。
- 决定：Reader 的 Kafka ClientID 设为 `im-<instanceID>`，`kafka-consumer-groups.sh --describe` 才能看出分区在哪个实例手里。
- 决定：`Refresh` 先 `EXPIRE`，key 不存在（被竞争中的 Offline 删了）时补 `SET`，避免在线用户一直显示离线。
- 决定：通话信令的 conv_id 由服务端按 (from, to) 计算，保证 Kafka key 稳定。
- 决定：只改了 main.go 的 switch（加 `case "kafka"`），Transport interface / Hub / MessageService 没有为 Phase 4 改动。
- 验收（kafka 模式 A:8080 + B:8081，均为 -race 构建）：
  - [x] 跨实例单聊/群聊互通 — 群聊：order_check 三个用户分布在 8080/8081；单聊：`API_B=http://localhost:8081 phase5_smoke.sh` 中 8080 上的 alice 与 8081 上的 bob 互发 file 消息、信令，全部 PASS
  - [x] order_check — `order_check` → `ORDER CHECK PASSED: 3 clients saw the same 900 seqs in the same strictly increasing order`
  - [x] 无重复落库 — `SELECT COUNT(*), COUNT(DISTINCT client_msg_id)` → `900 900`
  - [x] im-persist 故障转移 — `--describe --group im-persist` → 分区在 `im-B`；杀掉 B 后经 A 发 300 条 → 约 20s（consumer group 会话超时）后 A 接手，表内 901→1201 行、1201 个不同 client_msg_id，不丢（另 5 个 offset 是 Phase 5 的 call_* 信令，按设计不落库）
  - [x] 在线状态 — 用户连 B，经 A 查 `/api/presence` → true；正常断开 → 立即 false；连接期间直接杀掉 B → key TTL 88s，92s 内（5s 轮询粒度）变 false
  - [x] 切回 local 单实例 — 先 `TRUNCATE messages`（**原因：local 的 seq 来自 dispatcher 计数、kafka 的 seq 来自 offset+1，两种来源会撞主键，INSERT IGNORE 会静默丢消息**），不改代码 `-mode local` 启动，`phase23_smoke.sh` 8/8、`phase5_smoke.sh` 14/14 PASS
  - [x] `go vet ./... && go build ./...` 通过；-race 下的两实例测试后，两台服务端日志 `DATA RACE` 计数均为 0
  - 注：上面的 Kafka 验收是在把地址从 localhost 改成 127.0.0.1 **之前**跑的（当时 order_check 900 条耗时 13s，就是 40ms 往返造成的）；改后又复跑了一遍，见文末“复验”。

## Phase 5 — 文件传输 + 通话信令  状态：已通过
- 决定：只对 `call_invite` 查被叫在线状态；离线时回 `{type:"call_reject", from:被叫, to:主叫, payload:{reason:"offline"}}`，前端可按“对方拒绝”同一逻辑处理。其余信令照常 Publish（对方不在线就自然丢弃）。
- 决定：file 消息的 url 必须以 `/files/` 开头，且不含 `..` 和 `//`。
- 决定：`/files/*` 响应加 `X-Content-Type-Options: nosniff` 和 `Content-Security-Policy: sandbox`，防止上传的 HTML/SVG 在同源下执行。
- 决定：MaxBytesReader 上限 = 20MB + 64KB（multipart 边界开销），文件本体是否超 20MB 由 FileService 精确判断；存储名 `uuid + 清洗过的扩展名`，原文件名只用于展示。
- 决定：**生产环境需要换对象存储**（现在所有实例共用本机 `./uploads`，只适合单机开发）。
- 验收：
  - [x] 5MB 图片上传 + md5 — `phase5_smoke.sh` → url 以 /files/ 开头、mime `image/png`、从另一实例下载 md5 一致；21MB → 413
  - [x] 伪造 `url: "http://evil.com/x"` 被拒 — 收到 error `file url must start with /files/`
  - [x] kafka 模式跨实例完整信令往返 — alice@8080 ↔ bob@8081：invite → accept → offer → answer → hangup 全部送达，SDP 原样
  - [x] 呼叫离线用户 → 立刻收到 `call_reject` + `reason:"offline"`
  - [x] messages 表无 `call_%` 行 — `SELECT COUNT(*) ... LIKE 'call\_%'` → 0；file 消息落库 1 行
  - [x] `go vet ./... && go build ./...` 通过

## Phase 6 — Vue 前端  状态：代码完成，`npm run build` 通过；浏览器验收待用户执行
- 决定：没有用 `npm create vite` 交互式脚手架，手写等价文件；依赖为 npm 当前最新：vue 3.5、vite 8、pinia 4、vue-router 5、uuid 14。
- 决定：监听 `window` 的 `offline` 事件主动关闭 WS（DevTools 的 Offline 不会关闭已有 WebSocket），`online` 时立即重连。
- 决定：“5 秒无回显”从消息真正写进 socket 时开始计时，离线排队的时间不算。
- 决定：重连成功时在 `onopen` 里**同步**记下各已加载会话的 maxSeq 再补拉，避免重连后先到的实时消息把 maxSeq 推高而漏掉中间一段。
- 决定：最近单聊列表（只存对方 uid）放 sessionStorage；消息本身只在内存，不用 localStorage。
- 决定：没有“按会话倒序拉最新 N 条”的接口，打开会话时用 after_seq 分页拉完全部历史（后端接口不改）。
- 验收：
  - [x] `npm run build` → `✓ built`，无报错（有一条 INEFFECTIVE_DYNAMIC_IMPORT 提示：http.js 故意延迟 import router 以避开循环依赖，不影响功能）
  - [ ] 需要用户在浏览器操作（步骤见下方“Phase 6 浏览器验收步骤”）

### Phase 6 浏览器验收步骤（请执行后回报）
准备：`docker compose -f deploy/docker-compose.yml up -d`，然后在 `server/` 下 `go run ./cmd/server`，在 `web/` 下 `npm run dev`，打开 http://localhost:5173 。
1. 普通窗口注册 `alice1`，无痕窗口注册 `bob1`。
2. alice 左侧搜索 `bob1` → 点击 → 发一条文字，bob 窗口左侧出现会话并收到；alice 的消息先灰色“发送中…”再变正常。
3. alice 输入群名点“建群”，记下标题栏的群号；bob 输入群号点“加群”；双方互发群消息，气泡上方显示发送者名字。
4. 在群里点 📎 发一张图片（应显示缩略图），再发一个 PDF（应显示文件名、大小、“下载”链接）。
5. 在单聊里点“📹 视频通话”，另一窗口点“接听”，确认双向画面；点“挂断”后两边摄像头指示灯熄灭。
6. bob 窗口 DevTools → Network → Offline，保持 10 秒，期间 alice 发 5 条；bob 恢复 Online 后 5 条都出现、顺序正确、无重复（左上角状态会从“已断开，重连中…”变回“已连接”）。
7. kafka 模式：在 `server/` 下分别 `go run ./cmd/server -mode kafka` 和 `go run ./cmd/server -mode kafka -id B -addr :8081`（先 `bash deploy/init-topic.sh`，并清空 messages 表），在 `web/` 下分别 `VITE_BACKEND=localhost:8080 npm run dev -- --port 5173` 和 `VITE_BACKEND=localhost:8081 npm run dev -- --port 5174`，两个窗口分别开 5173/5174 重复 1–6，左侧在线绿点正确。

## Phase 7 — 压测  状态：已通过（发现的瓶颈未优化，等待用户决定）
- 决定：loadtest 是独立模块 `im/loadtest`，不 import 服务端代码；通过根目录 `go.work` 支持 `go run ./loadtest ...`。
- 决定：“成功 Publish”= 写出 − 写失败 − busy 错误 − 其他错误；应收按尝试发出的条数算，所以写失败/busy 都计入 loss。
- 决定：延迟用每连接私有的对数直方图（2% 精度），不存每条延迟；每轮用独立 runID 前缀区分预热/正式、区分轮次。
- 决定：服务端内存用 expvar `memstats.Sys` 代替 `ps -o rss`（Windows 的 Git Bash 没有 `ps -o`；脚本在 Linux/macOS 上仍会采 RSS）。
- 决定：`run_matrix.sh` 自己编译并启停服务端，每轮前 `TRUNCATE messages` 并等 10 秒，完整输出写到 `loadtest/logs/`。
- 验收：
  - [x] 连接测试 — local 单实例 `-conns 1000 -rate 100 -duration 300s` → 连接失败 0、掉线 0，goroutine 9 → 2033 → 33，memstats.Sys 120MB，loss 0
  - [x] 吞吐矩阵全部跑完 — `bash loadtest/run_matrix.sh` → `loadtest/results.md` 8 行（连接测试 + kafka 2000…12000 + local 8000 对照）
  - [x] README 写明 ceiling 和瓶颈层 — 投递 loss<0.1% 的上限 10,000 msg/s；要求落库不丢则 8,000 msg/s。10k 时 persister 溢出（persist_dropped 13%），12k 时 Windows 临时端口耗尽（MySQL 连接抖动）连锁崩溃（loss 25%，p99 20.8s）。未采集 CPU，已在 README 注明
  - [x] 简历数字核对 — 1,000 连接 / 8,000 msg/s / <0.1% loss 三项均被实测支持（kafka 两实例 8000 msg/s：loss 0.0000%，p99 24.8ms，无 busy/踢人/落库丢弃）；建议注明“同机部署、20 人群”

## 复验（改为 127.0.0.1 并重建 Kafka 之后）
- [x] kafka A+B（-race）：`order_check` → PASSED，900 条耗时 2.3s（改前 13.3s）；`COUNT(*)/COUNT(DISTINCT)` → 900/900
- [x] `API_A=:8080 API_B=:8081 phase5_smoke.sh` → 14/14 PASS；两台服务端日志 `DATA RACE` 计数 0
