# Phase 1 — 分层后端 + MySQL

## 目标
注册、登录（JWT）、建群/加群、查群成员、拉历史消息的 REST API，严格 api → service → dao 三层。

## 前置
Phase 0 已通过；docker compose 在运行。

## 任务

1. `internal/model`：gorm 模型 + `Envelope`（字段见 CLAUDE.md）。表结构必须等价于：

   ```sql
   CREATE TABLE users (
     id BIGINT AUTO_INCREMENT PRIMARY KEY,
     username VARCHAR(32) NOT NULL UNIQUE,
     pwd_hash VARBINARY(60) NOT NULL,
     created_at DATETIME(3));
   CREATE TABLE `groups` (
     id BIGINT AUTO_INCREMENT PRIMARY KEY,
     name VARCHAR(64) NOT NULL,
     owner_id BIGINT NOT NULL,
     created_at DATETIME(3));
   CREATE TABLE group_members (
     group_id BIGINT, user_id BIGINT,
     PRIMARY KEY (group_id, user_id),
     INDEX idx_user (user_id));
   CREATE TABLE messages (
     seq BIGINT PRIMARY KEY,           -- 不自增！由 Transport 生成（Phase 3/4）
     conv_id VARCHAR(40) NOT NULL,
     sender_id BIGINT NOT NULL,
     type VARCHAR(16) NOT NULL,
     content TEXT NOT NULL,            -- Envelope.Payload 原样存 JSON
     client_msg_id CHAR(36) NOT NULL UNIQUE,
     created_at DATETIME(3),
     INDEX idx_conv_seq (conv_id, seq));
   ```

   启动时 `AutoMigrate`。

2. `internal/dao`：`UserDAO`、`GroupDAO`、`MessageDAO`，只做 CRUD。`MessageDAO` 需要：`ListAfter(convID, afterSeq, limit)`、`MaxSeq()`、`BatchInsertIgnore(msgs)`（后两个 Phase 3 用，现在先写好）。
3. `internal/service`：
   - `UserService.Register / Login`：bcrypt 存密码；登录签 JWT（claims: `uid`，有效期 7 天）。
   - `GroupService.Create / Join / Members`：建群时创建者自动入群。
   - `ConvService`：`ConvID` 工具函数（单聊 `u_{小id}_{大id}`，群聊 `g_{id}`）；`CanAccess(uid, convID)`：单聊判断 uid 在 id 里，群聊判断是否群成员。
   - `MessageService.History(uid, convID, afterSeq, limit)`：先 `CanAccess`，limit 默认 50、最大 200。
4. `internal/api`：
   - JWT middleware：从 `Authorization: Bearer <token>` 或 query `token`（给 WS 用）取 token，`c.Set("uid", uid)`，失败 401。
   - 路由：

     | Method | Path | 鉴权 |
     |---|---|---|
     | POST | /api/register | 否 |
     | POST | /api/login | 否 |
     | GET | /api/me | 是 |
     | POST | /api/groups | 是 |
     | POST | /api/groups/:id/join | 是 |
     | GET | /api/groups/:id/members | 是 |
     | GET | /api/my/groups | 是 |
     | GET | /api/users?q= | 是（按用户名搜人，用于发起单聊） |
     | GET | /api/messages?conv_id=&after_seq=&limit= | 是 |

   - 统一错误格式：`{"error": "<code>", "message": "<人话>"}`。
5. `main.go` 组装：db → dao → service → api。

## 不要做
- 不写 WebSocket（Phase 2）。
- 不在 api / service 里直接用 `*gorm.DB`。

## 验收
- [ ] 写 `scripts/phase1_smoke.sh`（curl）：注册两个用户 → 登录拿 token → 建群 → 第二人加群 → 查成员有 2 人；脚本跑通
- [ ] 不带 token 访问 `/api/my/groups` 返回 401
- [ ] 非群成员请求 `/api/messages?conv_id=g_<id>` 返回 403
- [ ] `grep -rn "gorm.DB\|db\." internal/api internal/service` 无结果
- [ ] `go vet ./... && go build ./...` 通过

完成后更新 `PROGRESS.md`，停下等用户确认。
