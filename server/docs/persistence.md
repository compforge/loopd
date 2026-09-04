# loop-server Persistence

loop-server 当前只持久化 Conversation 与 Message 两类聊天事实。执行状态由 Operator CRD 或 Harness
持有；AgentLedger 后续保存运行、审计和成本事实，不替代聊天记录。

## Conversation

`conversations` 表包含：

- `id`：UUIDv7 主键；
- `name`：会话的可读名称；
- `parent_message_id`：可空；Operator 详情会话引用的主链路 Message ID；
- `created_at`、`updated_at`：记录时间。

主会话的 `parent_message_id` 为 `NULL`。详情会话只允许引用主会话中的 Message，不继续嵌套；同一条
Message 在 v1 中最多关联一个详情会话。

## Message

`messages` 表包含：

- `id`：UUIDv7 主键，同时作为消息读取游标；
- `conversation_id`：所属 Conversation；
- `kind`：发送者类型，只能是 `user`、`operator`、`harness`；
- `key`：发送者在所属系统中的稳定标识；
- `content`：AgentUE semantic model JSON；其中 `blocks` 按顺序承载 `text`、`tool`、`artifact` 等可扩展内容；
- `created_at`：记录时间。

一次问答在同一 Conversation 中表现为两条 Message：Human 的问题是 `kind=user`，最终回答是
`kind=operator` 或 `kind=harness`。每条 Message 的 content 都是完整语义快照，而非纯文本；最小结构为：

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

block 除 `id` 和 `type` 外的字段由 biz 扩展。运行进度、流式 delta 和审计事件不作为独立 Message；后续
执行层负责持久化 patch，并在终态或恢复时生成同一份 content 快照。

## UUIDv7 游标

消息列表使用 `id > after ORDER BY id` 翻页，不维护额外 sequence。go-stdx 的 `uuid.V7()` 在当前
loop-server 单实例进程内提供单调的时间有序 ID，适合 SQLite v1。

UUIDv7 的时间顺序不等于多节点数据库的全局提交顺序。引入多实例写入前必须重新确认游标语义；不能
仅凭不同进程生成的 UUIDv7 推断严格的全局先后关系。
