# Phase 7 — 压测

## 目标
写一个 Go 压测工具，实测：
1. 能否稳定保持 1,000 个并发 WebSocket 连接；
2. kafka 模式两实例下的吞吐上限（loss < 0.1% 时的最大 msg/s）。

简历数字只能来自本 phase 的实测结果。**不许为了凑 8,000 调低定义或改计算方式。**

## 前置
Phase 6 已通过。

## 定义（写进 `loadtest/README.md` 开头）

- **msg/s** = 所有发送连接每秒成功 Publish 的消息条数（发出条数，不是投递次数）。
- **应收次数** = 发出条数 × 该群在线成员数（含发送者自己的回显）。
- **loss** = 1 − 实收次数 / 应收次数。busy 被拒、unavailable、慢客户端被踢导致没收到，全部算丢失。
- **延迟** = 接收端收到时间 − payload 里的发送时间戳（同一台机器，时钟一致）。

## 任务

1. `loadtest/main.go` 两个子命令：

   ```bash
   # 准备数据：注册 u0001..u1000（密码统一），按 group-size 分群并入群，结果写 loadtest/fixture.json
   go run ./loadtest seed -users 1000 -group-size 20 -api http://localhost:8080

   # 压测
   go run ./loadtest run \
     -urls ws://localhost:8080/ws,ws://localhost:8081/ws \  # 连接轮流分配到各实例
     -conns 1000 -rate 8000 -duration 60s -warmup 10s
   ```

2. `run` 的行为：
   - 登录并建立全部连接，等全部 open 后开始计时；统计连接失败数。
   - 用 `golang.org/x/time/rate` 控制**总**发送速率，发送任务随机分给连接（随机群、随机成员），payload：`{"t":"lt","sent_at":<ns>,"pad":"<凑到 200 字节>"}`。
   - 每个连接的读 goroutine 记录收到的 `client_msg_id` 和延迟；warmup 期间的消息不计入统计。
   - 结束后等 5s 收尾，输出一行 JSON + 人类可读表格：连接数、连接失败数、目标速率、实际发出 msg/s、应收、实收、loss、p50/p99/max 延迟、busy 错误数。
   - 运行前后各抓一次服务端 `/debug/vars` 和 `/debug/goroutines`，把差值一起输出。
3. `loadtest/run_matrix.sh`：依次跑下面的结果表，每轮之间清空 messages 表并等待 10s，把输出追加到 `loadtest/results.md`。
4. 在 `loadtest/README.md` 写明：机器型号、CPU、内存、OS、Go 版本、是否与服务端同机。

## 测试矩阵

1. **连接数测试：** local 模式单实例，`-conns 1000 -rate 100 -duration 300s`。记录：连接失败数、goroutine 数、服务端 RSS 内存（`ps -o rss`）。
2. **吞吐测试：** kafka 模式两实例，`-conns 1000`、群大小 20，`-rate` 依次 2000 / 4000 / 6000 / 8000 / 10000 / 12000，每档 60s。
3. **对照组：** local 模式单实例，同样参数跑 8000。

结果表模板（`loadtest/results.md`）：

| 模式 | 实例数 | 连接 | 群大小 | 目标 msg/s | 实际 msg/s | loss | p99 延迟 | busy 数 |
|---|---|---|---|---|---|---|---|---|

## 准备（提示用户执行）

```bash
ulimit -n 65535   # 每个连接占 1 个文件描述符；服务端和压测端终端都要设置
```

## 不要做
- 不为了压测结果修改业务逻辑或锁定决策。发现瓶颈 → 记录在 `loadtest/README.md` 的「瓶颈分析」，停下问用户是否要优化。

## 验收
- [ ] 连接测试：1,000 连接保持 5 分钟，连接失败数 0，goroutine 数约 2,000+
- [ ] 吞吐矩阵全部跑完，`results.md` 填满
- [ ] README 写明：ceiling 是多少 msg/s；卡在哪一层（依据 `/debug/vars` 的 busy_rejected、slow_kicked、Kafka consumer lag、CPU 占用判断）
- [ ] 输出一段「简历数字核对」：简历写的 1,000 连接 / 8,000 msg/s / <0.1% loss 各自是否被实测支持；不支持的给出实测值，建议用户改简历

完成后更新 `PROGRESS.md`，项目完成。
