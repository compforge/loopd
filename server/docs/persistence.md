# loop-server 持久化

本文定义页面可见事实的存储归属与身份：数据库保存 Conversation、Message 和 Operator/Harness
在线注册，以及 Harness Run 调用记录和通用 resource_locks 租约（见 [Harness](../../docs/harness.md)）。同一份 Message 同时支持页面历史与参与者消费，不另建一份业务消息 queue 表。
消费协议由 [Conversation](conversation.md) 定义，跨存储的责任分层见 [Kernel](../../docs/kernel.md)。

数据库差异由 repo 的 GORM Dialector 封装，使用 DATABASE_DRIVER 与 DATABASE_DSN 配置。
Quick Start 默认临时 SQLite，持久部署由使用方提供外部 MySQL。Redis 是事件交付桥，不是数据库
或业务执行状态的替代品。loopd 不管理外置数据库实例。

## 会话归属

Conversation 是一个对话框，actor_kind/actor_key 表达组织归属，不限制谁可以在其中发言。

- **User conv**：用户组织的主会话，parent_id 为空，供多个参与者持续交流。
- **Operator conv**：Operator 的内部协作会话，parent_id 指向 User conv，归属于指定 Operator。
  多个 Harness 和其他 Actor 可以在其中发言，避免过程淹没主会话。

两者是习惯用语，不是两套模型。Harness 组织的工作会话使用自身 actor_kind，不伪装成 Operator。
server 在主会话接收定向消息时，按 parent_id + actor_kind + actor_key 创建或复用内部会话，
同一 Actor 跨多次输入共享详情。Chat、Speak（含流式首写）与 Human 答复均在消息事务内完成
分配；先锁定父会话行，唯一关系在多个 server 实例间复用，任一步失败则消息与新会话一起回滚。
广播、面向 User 的消息不分配过程会话；子会话中的消息不会递归创建新的过程会话。
Conversation 不保存 task_id，也不依赖某条回答先存在。

提交后 server 只读取已分配的子会话，将其 ID 与收件信号一起投影到主 Conv CRD，失败保留
待通知标记重试。关联缺失视为写入契约未满足，通知不代为创建。Operator 无需创建会话的 Verb
或 HTTP 接口。

左侧导航只列主会话。选中消息后，按其会话与 Actor 查找内部会话；用户消息使用收件 Actor，
其他消息使用发言 Actor。右侧显示该内部会话的消息，而不是按一次输入截出一份执行日志。

## Message 是发言事实

Message 用 kind/actor_key 表达发送者，target_kind/target_key 表达收件者。收件者均为空字符串
表示对会话发言。reply_to_id 指向同一会话的具体消息，不表达任务身份或执行顺序。

人可以连续追加消息，Operator 可以分多次回应；没有一问一答约束，也不预建空回答。
Speak 的稳定 Key 以 Conversation + Actor 为范围，存储层生成全局唯一 output_key。
同身份重试返回已有消息，新发言使用新 key；TaskID 不参与发言幂等身份。

自定义 Operator 角色使用完整 kind/key 发言和定向接收，kind 长度上限 128；在线服务发现仍由
Operator/Harness Registry 管理，不因出现新角色而自动注册服务。

消息保存 AgentUE semantic model JSON，包含 version、biz、meta 与 blocks。文字、工具展示、
文件等可以共存一条消息；delta 不另建 Message。完整 prompt、工具原始输入输出、重试与成本
属于执行轨迹，只有页面需要展示的部分进入 Message。

revision 表示可见快照版本，流式输出对应 AgentUE seq，Human 状态转换在事务内递增。
每条消息分别更新，不能因为 block ID 相同就跨消息合并。持久化与 replay 见
[页面交付](ue.md#页面交付)。

task_id 仅保存在真实用户 input 上作为提交交付标识，其他 Actor 发言不需要关联它；页面流不依赖该字段。
不再保存页面关闭意图。Message.status 列记录 streaming/completed/failed/cancelled/expired，
只表示这条消息的发送状态，不表示业务完成。默认 Speak、用户输入和 Human 卡片直接 completed；非流式 Speak 可指定终态 Status，
错误发言将 AgentUE meta.error 和 failed 状态在创建事务中一同保存；
流式输出从 streaming 开始，End 的终态与 Revision 一起保存；长期失活由 server 按 updated_at + TTL 收口为 expired。
受控 meta.output 只保存最后一次事件指纹，用于辨别响应丢失后的重试，不承担执行检查点。
output、human_request、human_reply 分别表达普通输出、交互问题和卡片答复，不指定唯一主回答。

Human 答复接受事务同时保存问题的最终选择，以及答复自身所需的问题快照；两条 Message
各自可独立呈现，不新增卡片表或读取时关联富化。具体内容契约见 [交互卡片](ue.md#自包含的消息呈现)。

## Message 内容与 Parts

AgentUE 1.1 的 `blocks` 可以混合内联 `{id, type, ...}` 与引用 `{id, ref}`。
loopd 的 `biz=chat` 存储关联由 server 管理：`ref` 是 `message_parts.id`，
解析必须同时匹配所属 `message_id` 和 block ID。引用只有 `id/ref`；type、正文、层级等字段
只保存在完整 block 中。数组位置决定显示顺序，Part 的创建或更新时间不决定顺序。

`message_parts` 是 Message 的物理存储，不是另一类协作事实，也不进入 CRD：

| 字段 | 含义 |
| --- | --- |
| id | UUIDv7 主键，作为不透明引用 key |
| message_id | 所属 Message，建立索引 |
| content | `{"blocks": [...]}`，包含一批完整 block，不允许嵌套引用 |
| size_bytes | content 序列化后的字节数 |

新消息先内联。达到 block 数量或正文总字节预算后，后续 block 使用引用；已有内联 block
因 append 增长超限也可外置。已经外置的 block 不自动搬回。部署可通过以下正整数参数调整：

| 环境变量 | 默认值 | 作用 |
| --- | --- | --- |
| MESSAGE_INLINE_BLOCKS | 32 | 内联前缀的最大 block 数量 |
| MESSAGE_INLINE_BYTES | 65536 | 内联 block JSON 的累计字节预算 |
| MESSAGE_PART_BYTES | 262144 | 一个 Part 的目标字节数 |

Part 按内容量容纳完整 block。新 block 优先放入尾部 Part；旧 block 增长导致所在 Part
超过目标大小时，把该 block 移到独立 Part，并原子更新引用。单个 block 自身超过目标大小时
允许独占一个更大的 Part，不截断正文，也不改变 AgentUE block 身份。上述大小是装箱预算，
不是 MySQL JSON 列上限；meta、引用目录以及单个超大 block 仍需受部署的实际容量约束。

流式投影先锁 Message，校验 revision、事件指纹和 status；按 block ID 只加载目标 Part，
应用 AgentUE reducer 后写入变更 Part、引用、meta 和 revision。同一事件在同一事务中全部
提交或回滚。metadata/End 不需要加载外置正文，幂等重试不再次 append。普通交付只读取消息
状态，桥初始化或断档修复时才加载完整快照；DB 提交后继续按现有规则尝试 Redis 交付。

存储引用不接受客户端或 Harness 自行构造。写入入口接受完整 AgentUE 内容，server 决定
存储位置。Speak 重试、历史、Poll、Human 与页面快照均在 server 展开引用后返回完整内容；
读取 Message 与 Parts 使用同一个数据库快照，列表批量加载 Parts。缺失 Part、错误归属、
错误 block ID 或嵌套引用作为存储错误返回，不能显示为空正文。

已有 1.0 内联数据直接读取，不需要全库回填；后续写入触发外置时升级为 1.1。
新建的 server/runtime 快照默认使用 1.1。schema 升级只新增 Part 表；旧版 server 不理解
引用，因此出现外置数据后，回退应用版本前必须先展开这些数据。

整条内容替换时删除不再引用的 Part，并清理保留 Part 中已经移除的 block；
普通 `DeleteMessage` 在同一事务内删除 Message 与全部 Parts；Harness Run 拥有的消息拒绝单独
替换或删除，须与调用记录协调保留与清理（见 [Harness](../../docs/harness.md)）。未来会话清理应复用相同事务原则，
不能只删除父行而留下 Parts。领域 CRD 的清理仍不决定聊天历史保留时间。

当前 HTTP/SSE 与 Runtime 继续接收完整模型。分片减少正文更新量，但不等于前端懒加载，
也不限制完整历史响应的大小；AgentUE 引用解析属于持久化层，本次不引入页面 Part API。

存储边界止于 repo：service、runtime 与页面始终读写完整 Message，不解释 ref 或分配 Part。
交付层可用 `GetMessageState` 仅查询寻址、revision 与完成状态；该返回类型不含 content。
需要恢复流快照时通过 `GetMessage` 获取完整内容。输入内容错误由 repo 返回通用内容错误，
HTTP 层映射为 400；持久数据缺失或损坏仍按存储故障处理。

## Human 状态

问题与答复继续以 Message 为唯一事实来源，不新增 Interaction 表或 CRD。
受控 ask/confirm block 保存状态和 deadline，meta 保存 EffectKey 与请求指纹。
human_due_at、wake_pending 是维护循环使用的投影，不另存问题正文。

问题以 Conv + Actor + EffectKey 复用，期限不因重试重置。有效答复原子创建 user Message，
收口问题并留下定向通知；重复相同答复幂等，矛盾或迟到答复拒绝。超时不伪造 user Message。

reply_to_id 是答复关联的唯一依据，不能用最近消息、相邻位置或 task_id 猜测。
问题生命周期独立于 UI delivery；关闭页面流不取消问题，普通发言不自动表示批准。
类型化契约见 [Runtime](../../docs/runtime.md)。

## 时间与分页

表主键使用 UUIDv7，消息按 id 排序并分页，不维护额外 sequence。
UUIDv7 的时间顺序不是多节点数据库的全局提交顺序；当前采用人类输入通常有先后的假设，
严格消费顺序的限制见 [Conversation](conversation.md)。

created_at 与 updated_at 表达首次到最后一次可见活动。updated_at 使用 server 实际接受新输出的时间，
不跟随 Harness 的历史或未来时间戳；读取与幂等重试不刷新它。过期只改变 status/revision，
保留最后活动时间与正文。DB 和 Redis 的 TTL 独立推进，容许短暂不一致，详见 [失活与 TTL](ue.md#失活与-ttl)。

时间区间如何用于并行展示见 [消息呈现](ue.md#消息呈现)，不由存储层规定页面布局。

## AgentUE 快照与实时事件流

DB 与 Redis 保存的是同一输出的不同形态，Operator 和 Harness 输出遵守相同契约：

- **DB 保存 merge 后的 AgentUE 模型快照**：依次应用 set、append 等更新，Message.content
  及其物理 parts 保存当前 meta/blocks，Revision 标识已应用的位置；DB 不追加保存每一条原始事件。
- **Redis Stream 保存 append-only 的实时 AgentUE 事件记录**：每条新记录追加到流中，
  不原地修改旧记录。append-only 描述 Redis 日志的存储方式，记录中的 AgentUE op 仍可以
  是 set、append 或 end；对某个 block 的修改通过追加新事件表达。
- 正常写入先在 DB 事务中合并快照，再尽力向 Redis 发布对应增量。前端与实时观察者从 Redis
  消费事件，按 AgentUE 语义更新本地模型；不以逐 token 查询数据库代替实时流。
- 重连、Redis 丢失或增量缺口时，从 DB 读取当前快照并以 start 恢复状态，再继续消费事件。
  快照能够恢复当前内容，但不能还原被合并掉的每一步原始增量。Redis 历史保留受交付 TTL 限制，
  不作为永久执行审计。

因此，DB Revision 表达快照进度，Redis Stream ID 表达交付游标，两者不可互换。Harness 原生
执行的稳定回放仍由 Harness 与 Adapter 负责，不能从 DB 快照反推出完整原生轨迹。

实时交付有两个消费视角，共用按 Message ID 寻址的 Redis 流：

- `GET /v1/conversations/:conversation_id/stream` 聚合会话内消息，服务 UI。
- `GET /v1/harness/runs/:run_id/stream` 观察 Run 对应的输出消息，服务 Operator 的 Call 句柄。

Operator 的 Speak/Emit 与 HarnessRunner 均先保存 DB 快照，再经 MessageService 发布 Redis；
Run 接收后尽力初始化消息流，再返回句柄，使正常订阅直接进入 Redis。监听器不创建 Redis key。
DB 只承担历史、首次快照、低频元数据发现与断档补偿；实时 AgentUE 更新不由数据库轮询交付。
