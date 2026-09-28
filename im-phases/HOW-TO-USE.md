# 怎么用这套 phase 文件

1. 新建项目文件夹 `im/`，把本包里的 `CLAUDE.md` 和 `phases/` 放进去。
2. 在 `im/` 里打开 Claude Code（Claude Code 会自动读取 `CLAUDE.md`）。
3. 对 Claude 说：**「开始 Phase 0」**。
4. Claude 做完会跑验收、写 `PROGRESS.md`，然后停下。你检查一遍，没问题就说：**「继续，开始 Phase 1」**。
5. 重复到 Phase 7。

建议每个 phase 开一个 git branch，验收通过再 merge：

```bash
git checkout -b phase-0        # 开始前
git checkout main && git merge phase-0   # 验收通过后
```

| 文件 | 内容 |
|---|---|
| `CLAUDE.md` | 全局规则、锁定决策、目录结构、消息格式 |
| `phases/phase-0-setup.md` | docker compose、config、骨架 |
| `phases/phase-1-layered-backend.md` | API/service/DAO 三层 + MySQL |
| `phases/phase-2-websocket.md` | WS 网关、每连接 2 个 goroutine |
| `phases/phase-3-transport-local.md` | Transport 接口、channel 背压、批量落库 |
| `phases/phase-4-kafka-redis.md` | Kafka 单 partition、两实例、Redis 在线状态 |
| `phases/phase-5-file-and-call.md` | 文件上传、WebRTC 信令 |
| `phases/phase-6-vue-frontend.md` | Vue 3 前端 |
| `phases/phase-7-loadtest.md` | 压测，产出简历数字 |
