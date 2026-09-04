# AGENTS.md

## 项目定位与边界

server 是 loop-server 组件，当前只拥有 Conversation 与 Message 两类聊天事实。Operator CRD、Harness
运行状态、执行审计和成本记录不进入这两个模型；它们分别由 Operator、Harness 与 AgentLedger 持有。

## 代码地图与核心模块

```text
server/
├── server.go               # 组件组装与资源生命周期
├── internal/api/           # Hertz 适配；Conversation、Message、Chat handler 分文件，VO 集中在 view.go
├── internal/model/         # GORM model；一张表一个 Go 文件
│   ├── conversation.go     # conversations
│   └── message.go          # messages
├── internal/repo/          # 数据库连接及按表拆分的持久化操作
│   ├── conversation.go
│   └── message.go
├── internal/service/       # 用例层；每类能力一个 Service
│   ├── conversation.go     # ConversationService
│   ├── message.go          # MessageService
│   └── chat.go             # ChatService；一次问答的事务边界
└── docs/persistence.md     # 两表 schema 与 UUIDv7 游标约束
```

## 关键约定

1. 依赖方向固定为 `api → service → repo/model`；API DTO 与 GORM model 不得跨层泄漏。
2. `model/` 一张表一个文件，`repo/` 按同名模型拆分；不要重新聚合成巨型 store 文件。
3. Conversation 使用 `name`。Message 使用 `task_id + kind + key + content`：kind 只能是 `user`、
   `operator`、`harness`，key 是该参与者的稳定标识，content 是页面可见的 AgentUE semantic model JSON，
   不是完整执行日志。
4. 两张表的 `id` 都由 service 使用 go-stdx `uuid.V7()` 生成。消息按 UUIDv7 字典序读取和翻页，不增加
   `sequence` 字段。
5. 一次 Chat 请求由 `ChatService` 在同一事务内创建 user Message 和空的 responder Message，两者共享
   Runner `task_id`；对外只返回 responder Message，后续流式快照更新该记录。
6. `conversations.parent_message_id` 只用于把 Operator 详情会话挂到主链路 responder Message。主会话为空，
   详情会话不可继续嵌套，同一 Message 最多对应一个详情会话。
7. 页面不可见的 prompt、tool call/result、重试和成本等完整轨迹进入 AgentLedger，不扩张 Message schema。

## References

- `../AGENTS.md` — loopd 全局边界与代码地图
- `docs/persistence.md` — Conversation 与 Message 持久化设计
- `../docs/kernel.md` — loopd 稳定理念和参与者边界
