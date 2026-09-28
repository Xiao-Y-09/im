# 压测

## 定义

- **msg/s** = 所有发送连接每秒成功 Publish 的消息条数（发出条数，不是投递次数）。
- **应收次数** = 发出条数 × 该群在线成员数（含发送者自己的回显）。
- **loss** = 1 − 实收次数 / 应收次数。busy 被拒、unavailable、慢客户端被踢导致没收到，全部算丢失。
- **延迟** = 接收端收到时间 − payload 里的发送时间戳（同一台机器，时钟一致）。

实现细节（`run.go`）：

- “成功 Publish” = 正式计时窗口内写出的消息 − 写失败 − 收到 `busy` 错误 − 收到其他错误；实际 msg/s = 它 ÷ duration。
- 应收次数按**尝试发出**的条数算（包括被 busy 拒绝、写失败的），所以这些都会体现为 loss。
- 预热期（`-warmup`）发出的消息用不同的 `client_msg_id` 前缀，接收端直接忽略；每轮还有独立的 runID 前缀，不会串轮。
- 延迟用每个读 goroutine 私有的对数直方图（桶宽 2%）统计，结束后合并；p50/p99 误差 ≤ 2%。

## 测试环境

| 项 | 值 |
|---|---|
| 机器 | Gigabyte X870E AORUS PRO ICE（台式机） |
| CPU | AMD Ryzen 9 9950X3D，16 核 / 32 线程 |
| 内存 | 93.6 GB |
| OS | Windows 11 Pro 10.0.26200；MySQL/Redis/Kafka 跑在 Docker Desktop（WSL2）里 |
| Go | go1.26.4 windows/amd64 |
| 是否与服务端同机 | **是**：压测端、两个服务端实例、MySQL、Redis、Kafka 全在同一台机器上争用 CPU |

压测时间：2026-09-28。数据：1000 个用户，50 个 20 人群（`seed -users 1000 -group-size 20`）。
完整输出在 `loadtest/logs/`，结果行在 `loadtest/results.md`。

## 结果

| 模式 | 实例数 | 连接 | 群大小 | 目标 msg/s | 实际 msg/s | loss | p50 | p99 | busy | persist_dropped |
|---|---|---|---|---|---|---|---|---|---|---|
| local（连接测试 300s） | 1 | 1000 | 20 | 100 | 100 | 0% | 1.5 ms | 2.5 ms | 0 | 0 |
| kafka | 2 | 1000 | 20 | 2000 | 2000 | 0% | 5.1 ms | 8.0 ms | 0 | 0 |
| kafka | 2 | 1000 | 20 | 4000 | 4000 | 0% | 5.5 ms | 9.6 ms | 0 | 0 |
| kafka | 2 | 1000 | 20 | 6000 | 6000 | 0% | 6.1 ms | 12.7 ms | 0 | 0 |
| kafka | 2 | 1000 | 20 | 8000 | 8000 | 0% | 6.6 ms | 24.8 ms | 0 | 0 |
| kafka | 2 | 1000 | 20 | 10000 | 10001 | 0% | 9.6 ms | 51.6 ms | 0 | **92,659（13%）** |
| kafka | 2 | 904 | 20 | 12000 | 10442 | **25.3%** | 11.2 ms | **20.8 s** | 0 | 166,534 |
| local（对照组） | 1 | 1000 | 20 | 8000 | 8000 | 0% | 2.0 ms | 10.6 ms | 0 | 0 |

`persist_dropped` 不在 loss 的定义里（loss 只看实时投递），但它意味着这些消息**没有进 MySQL 历史**，所以单独列出。

### 连接数测试（local 单实例，1000 连接，300 秒）

- 连接失败 0，运行期间掉线 0。
- 服务端 goroutine：9（空闲）→ **2033**（1000 连接期间，每连接 2 个 + 常驻约 33）→ 33（全部断开后）。
- 内存：`ps -o rss` 在 Windows 的 Git Bash 下不可用，改用服务端 expvar `memstats.Sys`：**120 MB**（1000 连接 + 100 msg/s）。

## 瓶颈分析

**ceiling：kafka 两实例下，投递 loss < 0.1% 的最大实测速率是 10,000 msg/s；同时要求落库不丢，则是 8,000 msg/s。12,000 msg/s 时系统崩溃。**

依据（各轮前后 `/debug/vars` 差值、Kafka consumer lag、服务端日志）：

1. **≤ 8,000 msg/s：没有任何层饱和。** busy_rejected = 0、slow_kicked = 0、persist_dropped = 0，每轮结束时三个 consumer group（im-deliver-A / im-deliver-B / im-persist）lag 都是 0。p99 随速率缓慢上升（8 → 25 ms），主要是同机 CPU 争用 + Kafka Writer 5ms 攒批。
2. **10,000 msg/s：落库层先到顶。** 实时投递仍然 0 丢失（lag 0、slow_kicked 0），但持有 im-persist 的实例 B 的 persister 队列（8192）溢出，`persist_dropped` = 92,659（约 13%）。原因：只有一个 persister goroutine 串行做 200 条一批的 `INSERT IGNORE`，同机 MySQL（Docker Desktop）的写入吞吐约 1 万行/秒。
3. **12,000 msg/s：Windows 临时端口耗尽，引发连锁崩溃。** 服务端日志大量 `dial tcp 127.0.0.1:3306: Only one usage of each socket address ... is normally permitted`，压测端也有 96 个连接因同样原因失败。原因推断：每条消息的 `CanAccess` 都查一次 MySQL（12k qps），而连接池 `MaxIdleConns(25) < MaxOpenConns(50)`，高峰时连接被反复关闭再新建，留下大量 TIME_WAIT 占满临时端口。随后 CanAccess 失败（other_errors 5,021）、接收端积压被踢（slow_kicked 163）、写失败 88,649，p99 到 20.8 秒。busy_rejected 始终为 0：kafka 模式的背压只体现在 Publish 变慢，不会产生 busy。
4. **CPU：** 本次没有采集 CPU 占用（Windows 下 run_matrix.sh 没有现成的采样手段），上面的判断只依据计数器、lag 和日志。

可能的优化方向（**未实施**，按 phase-7 要求先问用户）：
- 群成员鉴权走 Hub 已有的成员缓存，或给 `CanAccess` 加短 TTL 缓存，去掉每条消息一次的 MySQL 查询；
- `SetMaxIdleConns` 与 `SetMaxOpenConns` 设为相同值，避免连接抖动；
- persister 多个写协程 / 更大批次，或把落库拆成独立服务。

## 简历数字核对

| 简历写法 | 是否被实测支持 | 依据 |
|---|---|---|
| 1,000 并发连接 | **支持** | local 单实例 1000 连接保持 300 秒，失败 0、掉线 0，goroutine 2,033 |
| 8,000 msg/s | **支持** | kafka 两实例 8,000 msg/s × 60 秒，实际 8,000 msg/s，同时无 busy、无踢人、无落库丢弃 |
| < 0.1% loss | **支持**（在 8,000 msg/s 下） | 同一轮 loss = 0.0000%（应收 9,600,440 次，实收 9,600,440 次） |

建议写法里带上前提，避免被追问时说不清：“单机部署（压测端与服务端同机）、20 人群、每条消息扇出 20 份，即约 16 万次投递/秒”。
10,000 msg/s 时实时投递同样 0 丢失，但有 13% 的消息没落库，**不建议**写 10,000。

## 用法

```bash
# 仓库根目录；先 docker compose up、init-topic，并启动服务端
go run ./loadtest seed -users 1000 -group-size 20 -api http://127.0.0.1:8080
go run ./loadtest run -urls ws://127.0.0.1:8080/ws,ws://127.0.0.1:8081/ws \
  -conns 1000 -rate 8000 -duration 60s -warmup 10s

# 整个矩阵（自己启停服务端、每轮清空 messages 表）：
bash loadtest/run_matrix.sh          # SKIP_CONN=1 跳过 5 分钟连接测试；RATES="2000 4000" 只跑部分档位
```

> Windows 注意：地址请写 `127.0.0.1` 而不是 `localhost`。本机 `localhost` 先解析到 `::1`，
> 经 Docker Desktop 的 IPv6 端口转发每次往返约 40ms，会把 MySQL/Kafka 变成瓶颈（见 PROGRESS.md）。
