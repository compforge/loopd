# loop-server Persistence

loop-server 数据库持久化 Conversation 与 Message 两类页面可见的聊天事实，并用 Operator/Harness
注册表支持在线 Actor 发现，但不建立 `tasks` 表。每次问答对应的 Task 是 Kubernetes CRD；AgentLedger
保存完整运行轨迹、审计和成本事实，不替代页面聊天记录。

loop-server 通过 `DATABASE_DRIVER` 选择数据库。Helm 在配置 MySQL 时注入对应 driver 与 DSN，
未配置时使用 SQLite。Quick Start 的 SQLite 位于临时卷，适合零依赖体验；需要跨 Pod 重建保留
聊天记录时，由部署方提供外部 MySQL，loopd 不创建
或管理数据库实例。Redis 只承载运行中的页面事件与断线续读，不是聊天事实源，因此内置 Redis 使用
内存存储。

数据库配置统一为 `DATABASE_DRIVER` 与 `DATABASE_DSN`。SQLite/MySQL 的 GORM Dialector 选择只发生在
repo 层，service 与 API 不感知数据库类型。

Operator/Harness 表的租约及发现语义见 [在线注册与发现](registry.md)。本文只维护聊天持久化约束。

## Conversation

`conversations` 表包含：

- `id`：UUIDv7 主键；
- `name`：会话的可读名称；
- `parent_message_id`：可空；Operator 详情会话引用的主链路 Message ID；
- `created_at`、`updated_at`：记录时间。

主会话的 `parent_message_id` 为 `NULL`。详情会话只允许引用主会话中的 response Message，不继续嵌套；
同一条 Message 在 v1 中最多关联一个详情会话。

## Message

`messages` 表包含：

- `id`：UUIDv7 主键，同时作为消息读取游标；
- `conversation_id`：所属 Conversation；
- `task_id`：对应 Task CRD 名称的一次完整问答标识；反问、确认等可见消息继续使用同一值；
- `kind`：发送者类型，只能是 `user`、`operator`、`harness`；
- `key`：发送者在所属系统中的稳定标识；
- `content`：页面可见的 AgentUE semantic model JSON；其中 `blocks` 按顺序承载 `text`、`tool`、
  `artifact` 等可扩展内容；
- `created_at`、`updated_at`：创建时间与可见内容最后更新时间。

一次 Chat 创建 Human 的问题 Message 和目标 Operator/Harness 的空回答，两者共享 task_id。
跨数据库、Redis 和 Kubernetes 的提交补偿、上下文查询与完成顺序见 [Task 交付](task-delivery.md)。

每条 Message 的 content 都是页面可见的完整语义快照，而非纯文本；最小结构为：

```json
{
  "version": "1.0",
  "biz": "chat",
  "meta": {},
  "blocks": [
    {"id": "answer", "type": "text", "content": "done"},
    {"id": "tool-1", "type": "tool", "name": "search", "status": "completed"}
  ]
}
```

block 除 `id` 和 `type` 外的字段由 biz 扩展。页面无需展示的 system prompt、tool call/result、模型原始
事件、重试与成本不进入 Message，由 AgentLedger 记录。流式 delta 也不作为独立 Message；AgentUE Redis
Bridge 承载运行中的页面事件，任务完成时由 server 将它们折叠为可恢复的 content 快照。

## UUIDv7 游标

所有表主键由 service 使用 go-stdx 的 `uuid.V7()` 生成。消息列表使用 `id > after ORDER BY id`
翻页，不维护额外 sequence。UUIDv7 在当前 loop-server 单实例进程内提供单调的时间有序 ID。

UUIDv7 的时间顺序不等于多节点数据库的全局提交顺序。引入多实例写入前必须重新确认游标语义；不能
仅凭不同进程生成的 UUIDv7 推断严格的全局先后关系。
