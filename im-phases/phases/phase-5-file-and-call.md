# Phase 5 — 文件传输 + 通话信令（后端）

## 目标
文件上传/下载 API；`file` 消息和 `call_*` 信令在两种 Transport 模式下都能跨用户、跨实例投递。前端在 Phase 6 做，本 phase 用脚本验证。

## 前置
Phase 4 已通过。

## 任务

1. 上传：`POST /api/upload`（鉴权，multipart 字段名 `file`）
   - 用 `http.MaxBytesReader` 限制 20MB，超了返回 413。
   - 存到 `{upload_dir}/{uuid}{原扩展名}`；返回 `{"url":"/files/<name>","name":"<原文件名>","size":<字节>,"mime":"<检测到的类型>"}`。
   - MIME 用 `http.DetectContentType` 检测，不信任客户端传的类型。
   - 所有实例共用同一个 `upload_dir`（本地开发两个进程在同一台机器上即可）；在 PROGRESS.md 记一行“生产环境需要换对象存储”。
2. 下载：`r.Static("/files", upload_dir)`。
3. `file` 消息：payload 必须包含 `url/name/size/mime`，`url` 必须以 `/files/` 开头，否则回 error。投递、落库和 `chat` 完全相同。
4. 通话信令：
   - 支持的 type：`call_invite`、`call_accept`、`call_reject`、`call_offer`、`call_answer`、`call_ice`、`call_hangup`。
   - 服务端不解析 payload（SDP / ICE 原样转发），只检查 `To` 非 0 且不等于自己。
   - 走同一个 `transport.Publish`，Hub 按 `To` 投递（Phase 2 已实现），persister 忽略（Phase 3 已实现）。
   - 如果被叫不在线（Redis presence 查不到），直接给主叫回 `{type:"call_reject", payload:{reason:"offline"}}`，不 Publish。
5. `scripts/phase5_smoke.sh`：上传一张图片 → wsclient 发 file 消息 → 另一端收到 → `curl` 下载比对 md5；再用 wsclient 模拟 `call_invite` → `call_accept` → `call_offer` → `call_answer` 往返。

## 不要做
- 不把文件字节放进 WebSocket 或 Kafka。
- 不做 TURN、不做录音录像。

## 验收
- [ ] 5MB 图片上传成功，下载后 md5 一致；21MB 文件返回 413
- [ ] file 消息伪造 `url: "http://evil.com/x"` 被拒
- [ ] kafka 模式下连 8080 的用户对连 8081 的用户完成一次完整信令往返
- [ ] 呼叫离线用户，主叫立刻收到 `reason:"offline"`
- [ ] MySQL messages 表里没有任何 `call_%` 类型的行
- [ ] `go vet ./... && go build ./...` 通过

完成后更新 `PROGRESS.md`，停下等用户确认。
