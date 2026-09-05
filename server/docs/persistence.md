# loop-server Persistence

loop-server 数据库持久化 Conversation 与 Message 两类页面可见的聊天事实，并用 Operator/Harness
注册表支持在线 Actor 发现，但不建立 `tasks` 表。Conv CRD 提供参与者定向通知与接收游标；AgentLedger
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

会话沿用以下习惯叫法：

- **User conv**：用户的主会话，`actor_kind=user`，`actor_key` 为用户标识，`task_id` 为空。
- **Operator conv**：Operator 组织的工作／详情会话，`actor_kind=operator`，`actor_key` 为
  Operator 的逻辑标识，`task_id` 为本次 Chat 交付 ID；其中可以有多个 Harness、Operator 或 User 的消息。

它们共用 Conversation 模型和表，不另设会话类型字段。工作会话若归属于 Harness，使用
`actor_kind=harness`，不把它的真实归属强行标为 Operator。

`conversations` 表包含：

- `id`：UUIDv7 主键；
- `name`：会话的可读名称；
- `actor_kind`、`actor_key`：会话组织者的类型与逻辑身份，不限制其中 Message 的发送者；
- `task_id`：可空；工作会话所属的 Chat 交付 ID；
- `created_at`、`updated_at`：记录时间。

User Conversation 的 `task_id` 为 `NULL`，actor 由 server 根据调用者身份确定。
每次提问可以更换 Operator/Harness，但不会改变会话归属。工作 Conversation 关联非空 Task ID，
actor 从该 Task 的目标推导，而不是从最近一条 Message 推断，也不是 Pod 或进程身份。
同一 Task 在 v1 中最多关联一个工作会话，多个 Harness 共享这个会话，不按参与者拆分。

左侧导航只列 User Conversation。选择其中的 Message 后，按它的 `task_id` 查找工作会话，
右侧展示该会话的 Message；没有工作会话就展示空状态。原始问题、反问、用户选择和最终回答
可以打开同一份处理详情，页面选中项仍使用 Message ID。

Conversation 只保存会话名称、组织归属等聊天信息，不承担 Task 完成意图、重试状态或 Operator
领域状态。Conv CRD 提供定向唤醒；读取聊天事实不依赖 CRD 仍然存活。

工作输出通过 task_id + output_key 唯一定位一条 Message，发送者不可变；同一 Actor 可以使用
不同 key 发布多条输出。内部 Harness Call 使用 EffectKey 作为输出 key，actor_key 使用 Call 身份。
同一次 Call 的文本和工具块保存在同一条 Message 中。effect_key 是可见步骤名，
不代替身份。主回答只保存 Operator 自己的输出，不混存这些 Harness Message。

## Message

`messages` 表包含：

- `id`：UUIDv7 主键，同时作为消息读取游标；
- `conversation_id`：所属 Conversation；
- `task_id`：一次 Chat 页面流及 Redis 交付标识，不对应业务 Task；反问、确认等可见消息继续使用同一值；
- `kind`：发送者类型，只能是 `user`、`operator`、`harness`；
- `actor_key`：发送者在所属系统中的稳定标识；
- `target_kind`、`target_key`：消息收件者；两列空字符串表示显式广播，历史 NULL 不推断为广播；
- `dispatch_pending`：消息提交后的 Conv 通知重试标记；
- `reply_to_message_id`：精确回复引用，未回复其他消息时为空；
- `purpose`：input/response 标记初始输入与主回答，output 标记独立工作输出，human_request/human_reply 标记交互消息；
- `output_key`：工作输出在 Task 内的稳定身份；与 task_id 组成唯一约束，其他消息为空；
- `revision`：消息持久快照版本；Human 交互由事务递增，流式输出保存已固化的 AgentUE seq；
- `human_due_at`、`wake_pending`：可索引的到期调度投影与持久通知标记，不另存问题或答案；
- `delivery_state`、`completion`：input 的交付收口状态与重试意图，用于协调交互和流关闭；
- `content`：页面可见的 AgentUE semantic model JSON；其中 `blocks` 按顺序承载 `text`、`tool`、
  `artifact` 等可扩展内容；
- `created_at`、`updated_at`：创建时间与可见内容最后更新时间。

一次 Chat 只创建真实的 user input；回答、Ask/Confirm 和工作输出在实际发起时各自创建 Message。
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
Bridge 承载运行中的页面事件，任务完成时由 server 按 Message 分别折叠为可恢复的 content 快照。

### Human 消息扩展

Message 的可选 `reply_to_message_id`，指向同一 Conversation 与 Task 中被答复的消息。
它是答复关联的唯一依据，与工作 Conversation 的 task_id 归属不同，也不定义执行顺序。
不能用消息相邻、时间顺序、Actor 或 task_id 代替引用；Human 答复缺少引用或目标无效时拒绝。
消息按时间展示不要求各 Actor 串行工作；一个 Task 可以并行产生多条提问，并按任意顺序收录答复。

沿用 content JSON，以 loopd 业务 block type `ask/confirm/human_reply` 表达问题、确认和用户
答复；问题的受控 meta 保存 EffectKey 等控制信息，block 保存请求状态与 deadline。问题和答复
本身就是 Message，不另存一份 Interaction 事实。每条消息独立保存 model；写入有效答复与问题
收口必须原子完成。

问题正文只存在于 block，受控 meta 只保存 EffectKey、Timeout 与指纹。调度投影在问题首次写入时
设置，在进入终态时与 block 状态一并更新；卡片答复的定向 Message 通过待通知标记更新 Conv；超时由 handle 读取或 deadline 调度感知。
超时只更新问题状态，主动忽略才形成 user 回复。类型与行为契约统一见
[Runtime](../../docs/runtime.md)。

## UUIDv7 游标

所有表主键使用 go-stdx 的 `uuid.V7()` 生成。消息列表使用 `id > after ORDER BY id`
翻页，不维护额外 sequence。UUIDv7 在当前 loop-server 单实例进程内提供单调的时间有序 ID。

UUIDv7 的时间顺序不等于多节点数据库的全局提交顺序。引入多实例写入前必须重新确认游标语义；不能
仅凭不同进程生成的 UUIDv7 推断严格的全局先后关系。

input 与实际发布的 response 分别在写入时标记 purpose。只有恰好一条 user 和一条非 user 的未标记主链路
可以作为无歧义存量数据读取；写入 Human 交互前在同一锁下固定这两个身份。其他多消息存量数据
拒绝猜测配对，需要先修复身份。新问题要求 input 的交付尚未开始收尾，不查询 Task CRD。
