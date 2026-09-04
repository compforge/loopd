# loop-server Persistence

loop-server 当前只持久化 Conversation 与 Message 两类页面可见的聊天事实。执行状态由 Operator CRD 或
Harness 持有；AgentLedger 保存完整运行轨迹、审计和成本事实，不替代页面聊天记录。

## Conversation

`conversations` 表包含：

- `id`：UUIDv7 主键；
- `name`：会话的可读名称；
- `parent_message_id`：可空；Operator 详情会话引用的主链路 Message ID；
- `created_at`、`updated_at`：记录时间。

主会话的 `parent_message_id` 为 `NULL`。详情会话只允许引用主会话中的 responder Message，不继续嵌套；
同一条 Message 在 v1 中最多关联一个详情会话。

## Message

`messages` 表包含：

- `id`：UUIDv7 主键，同时作为消息读取游标；
- `conversation_id`：所属 Conversation；
- `task_id`：Runner 对一次完整问答任务的标识；反问、确认等可见消息继续使用同一值；
- `kind`：发送者类型，只能是 `user`、`operator`、`harness`；
- `key`：发送者在所属系统中的稳定标识；
- `content`：页面可见的 AgentUE semantic model JSON；其中 `blocks` 按顺序承载 `text`、`tool`、
  `artifact` 等可扩展内容；
- `created_at`、`updated_at`：创建时间与可见内容最后更新时间。

一次 Chat 请求在一个数据库事务中创建两条 Message：Human 的问题是 `kind=user`，同时创建一条
`kind=operator` 或 `kind=harness` 的空回答。两条记录共享 `task_id`，HTTP 返回回答 Message；Operator 或
Harness 在流式处理过程中更新这条回答的 content。

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
事件、重试与成本不进入 Message，由 AgentLedger 记录。流式 delta 也不作为独立 Message；执行层将它们
折叠为页面可恢复的 content 快照。

## UUIDv7 游标

消息列表使用 `id > after ORDER BY id` 翻页，不维护额外 sequence。go-stdx 的 `uuid.V7()` 在当前
loop-server 单实例进程内提供单调的时间有序 ID，适合 SQLite v1。

UUIDv7 的时间顺序不等于多节点数据库的全局提交顺序。引入多实例写入前必须重新确认游标语义；不能
仅凭不同进程生成的 UUIDv7 推断严格的全局先后关系。
