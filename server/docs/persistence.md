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

持久化标识使用领域前缀，与 Go model 字段通过默认命名规则对应；公共 API 的 `kind/key` 表达不变。

Operator/Harness 注册与发现属于 [Runtime](../../docs/runtime.md) 的协作能力，租约与注册记录
约束由该文档统一维护。本文只维护聊天持久化约束。

## Conversation

`conversations` 表包含：

- `id`：UUIDv7 主键；
- `name`：会话的可读名称；
- `parent_message_id`：可空；Operator 详情会话引用的主链路 Message ID；
- `created_at`、`updated_at`：记录时间。

主会话的 `parent_message_id` 为 `NULL`。详情会话只允许引用主会话中的 response Message，不继续嵌套；
同一条 Message 在 v1 中最多关联一个详情会话。

左侧导航只列主会话。选择主会话中的 Message 后，按它的 ID 查找 `parent_message_id` 相同的详情
会话，右侧展示该会话的 Message；没有关联会话就展示空状态。Task ID 关联一次执行，不替代
这个父子关系，也不作为页面选中 Message 的标识。

Operator 内部的临时 Harness Call 各对应详情会话中的一条 `harness` Message，actor_key 使用
该 Call 的身份；同一次 Call 的文本和工具块保存在同一条 Message 中。effect_key 是可见步骤名，
不代替身份。主回答只保存 Operator 自己的输出，不混存这些 Harness Message。

## Message

`messages` 表包含：

- `id`：UUIDv7 主键，同时作为消息读取游标；
- `conversation_id`：所属 Conversation；
- `task_id`：对应 Task CRD 名称的一次完整问答标识；反问、确认等可见消息继续使用同一值；
- `kind`：发送者类型，只能是 `user`、`operator`、`harness`；
- `actor_key`：发送者在所属系统中的稳定标识；
- `reply_to_message_id`：精确回复引用，未回复其他消息时为空；
- `purpose`：input、response、human_request 或 human_reply，固定主链路身份；详情消息不使用这些用途；
- `revision`：Human Message 快照版本，用于独立交付和拒绝过期投影；
- `human_due_at`、`wake_pending`：可索引的到期调度投影与持久通知标记，不另存问题或答案；
- `delivery_state`、`completion`：主 response 的收口状态与重试意图，用于协调交互和 Task 删除；
- `content`：页面可见的 AgentUE semantic model JSON；其中 `blocks` 按顺序承载 `text`、`tool`、
  `artifact` 等可扩展内容；
- `created_at`、`updated_at`：创建时间与可见内容最后更新时间。

一次 Chat 创建 Human 的问题 Message 和目标 Operator/Harness 的空回答，两者共享 task_id。
跨数据库、Redis 和 Kubernetes 的提交补偿、上下文查询与完成顺序见 [Task 交付](task-delivery.md)。

详情 Message 的时间区间表达首次到最后一次可见输出。Runtime 在 Harness 输出中复用 AgentUE 的
毫秒时间戳；server 接受事件后更新该消息的区间，完成时固化 content 不改变活动时间。重试、乱序写入
和完成重放不会缩短区间，也不会把所有步骤的结束时间改成整个 Task 的完成时间。

右侧按相交的时间区间分组，每组有多少 actor 就展示多少列，同一 actor 的消息在列内纵向排列。
相连的重叠区间归为同组；与前组无交集的新时间段重新从左开始，不延续整段会话的列位。
端点相同也视作有交集。时间只决定分组，
卡片按内容自然高度展示，不按持续时间拉伸，也不强制对齐底部。区间代表输出活动，不是精确的 Harness
执行耗时或因果依赖。没有事件时间戳的消息保留数据库记录时间，不推测历史执行区间。

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

### Human 消息扩展

Message 的可选 `reply_to_message_id`，指向同一 Conversation 与 Task 中被答复的消息。
它是答复关联的唯一依据，与详情 Conversation 的 parent_message_id 归属不同，也不定义执行顺序。
不能用消息相邻、时间顺序、Actor 或 task_id 代替引用；Human 答复缺少引用或目标无效时拒绝。
消息按时间展示不要求各 Actor 串行工作；一个 Task 可以并行产生多条提问，并按任意顺序收录答复。

沿用 content JSON，以 loopd 业务 block type `ask/confirm/human_reply` 表达问题、确认和用户
答复；问题的受控 meta 保存 EffectKey 等控制信息，block 保存请求状态与 deadline。问题和答复
本身就是 Message，不另存一份 Interaction 事实。每条消息独立保存 model；写入有效答复与问题
收口必须原子完成。

问题正文只存在于 block，受控 meta 只保存 EffectKey、Timeout 与指纹。调度投影在问题首次写入时
设置，在进入终态时与 block 状态一并更新；唤醒标记只在成功通知 Kubernetes 后清除。
超时只更新问题状态，主动忽略才形成 user 回复。类型与行为契约统一见
[Runtime](../../docs/runtime.md)。

## UUIDv7 游标

所有表主键使用 go-stdx 的 `uuid.V7()` 生成。消息列表使用 `id > after ORDER BY id`
翻页，不维护额外 sequence。UUIDv7 在当前 loop-server 单实例进程内提供单调的时间有序 ID。

UUIDv7 的时间顺序不等于多节点数据库的全局提交顺序。引入多实例写入前必须重新确认游标语义；不能
仅凭不同进程生成的 UUIDv7 推断严格的全局先后关系。

主 input/response 在创建事务中标记 purpose。只有恰好一条 user 和一条非 user 的未标记主链路
可以作为无歧义存量数据读取；写入 Human 交互前在同一锁下固定这两个身份。其他多消息存量数据
拒绝猜测配对，需要先修复身份。新问题还要求 Task CRD 存在，不能给已退休的存量问答追加交互。
