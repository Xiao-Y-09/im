# Phase 6 — Vue 前端

## 目标
浏览器里完整走通：注册/登录 → 会话列表 → 单聊/群聊 → 发文件 → 音视频通话；断网重连后消息自动补齐。

## 前置
Phase 5 已通过。

## 任务

1. 初始化：

   ```bash
   cd web && npm create vite@latest . -- --template vue
   npm i pinia vue-router uuid
   ```

2. `vite.config.js` 代理（端口通过环境变量 `VITE_BACKEND`，默认 `localhost:8080`，方便连 B 实例测试）：

   ```js
   server: { proxy: {
     '/api':   `http://${backend}`,
     '/files': `http://${backend}`,
     '/ws':    { target: `ws://${backend}`, ws: true },
   }}
   ```

3. 文件结构与职责：

   | 文件 | 职责 |
   |---|---|
   | `src/net/http.js` | fetch 封装：自动带 token，401 跳登录 |
   | `src/net/ws.js` | 连接 `/ws?token=`；按 `type` 分发给订阅者；断线自动重连（1s, 2s, 4s … 上限 30s）；断开期间发送的消息进队列，重连后补发；暴露 `status`（connecting / open / closed） |
   | `src/stores/auth.js` | token、当前用户；token 存 sessionStorage |
   | `src/stores/chat.js` | `convs: { [convId]: { messages: [], maxSeq } }`；`pending: { [clientMsgId]: msg }`；`addIncoming(env)` 按 seq 插入并去重 |
   | `src/stores/presence.js` | 每 30s 批量查 `/api/presence` |
   | `src/views/Login.vue` | 登录 / 注册切换 |
   | `src/views/Chat.vue` | 左：会话列表（群 + 最近单聊，在线绿点）+ 搜索用户 + 建群/加群；右：消息列表 + 输入框 + 上传按钮 + 通话按钮（仅单聊） |
   | `src/components/MessageItem.vue` | chat 显示文本；file 是图片就显示缩略图，否则显示文件名 + 大小 + 下载链接；pending 灰色，失败显示“重发” |
   | `src/components/CallModal.vue` | 来电提示（接听/拒绝）、本地和远端两个 `<video>`、挂断 |
   | `src/call/webrtc.js` | `RTCPeerConnection`（STUN `stun:stun.l.google.com:19302`）；把 offer / answer / ICE 通过 ws.js 发成 `call_*` 消息 |

4. 关键逻辑（必须按这个实现）：
   - **发送确认：** 发送时生成 `client_msg_id`，消息先进 `pending` 并显示为发送中；收到同 `client_msg_id` 的回显 → 移出 pending、以服务端 seq 入列；收到 `busy`/`unavailable` 错误或 5 秒无回显 → 标记失败，点“重发”用**同一个** `client_msg_id` 再发。
   - **断线补齐：** ws 重连成功后，对每个已打开的会话调 `GET /api/messages?conv_id=&after_seq=<maxSeq>`，合并去重。
   - **通话流程：** 主叫 `call_invite` → 被叫 `call_accept` → 主叫创建 offer 发 `call_offer` → 被叫发 `call_answer` → 双方互发 `call_ice` → 任一方 `call_hangup` 关闭 PeerConnection 和摄像头。收到 `call_reject` 关闭弹窗并提示原因。
5. 样式保持简洁，不引入 UI 组件库。

## 不要做
- 不改后端接口；前端发现后端缺东西 → 停下问用户。
- 不用 localStorage 存消息。

## 验收
- [ ] 两个浏览器（一个普通窗口一个无痕窗口）完成：注册 → 单聊 → 建群 → 群聊 → 发图片（显示缩略图）→ 发 PDF（显示下载链接）
- [ ] 两个窗口之间视频通话接通、画面双向可见、挂断后摄像头指示灯熄灭
- [ ] DevTools → Network 设为 Offline 10 秒，期间对方发 5 条；恢复后 5 条都出现且顺序正确，无重复
- [ ] kafka 模式：起两个 vite dev server（`VITE_BACKEND=localhost:8080 npm run dev -- --port 5173` 和 `VITE_BACKEND=localhost:8081 npm run dev -- --port 5174`），两个窗口分别打开，以上功能全部正常，在线绿点正确
- [ ] `npm run build` 无报错

完成后更新 `PROGRESS.md`，停下等用户确认。需要用户亲自在浏览器操作的验收项，列出操作步骤请用户执行并回报结果。
