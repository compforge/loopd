# 用户交互与页面交付

本文描述 Actor 协作如何呈现在页面上：布局、消息呈现、Ask/Confirm 卡片，以及支撑这些体验的
流式交付、快照、重连与收尾。业务消费协议与存储事实分别由 Conversation、Persistence 文档拥有。

Actor 之间的协作依赖持久消息与 [Poll/Commit](conversation.md)，不依赖页面连接。
Redis 的 replay 位置不代表 Operator 已经消费，Operator 的 Commit 也不代表页面已经收到输出。
DB、CRD 与 Redis 的整体责任分层见 [Kernel](../../docs/kernel.md)。

## 页面布局

页面按左、中、右组织：左侧列出 User conv，中间展示选中会话的消息与发送框，右侧展示
server 在主会话接收定向消息时分配的内部会话，页面通过 User conv 与目标 Actor 查找。发送目标跟随发送框，每次发言可以选择不同参与者。

用户发送后，右侧立即按接收 Operator 查找工作会话，不等待它先在主会话回答；尚未创建时显示
该 Operator 的等待提示并持续查找。点击历史消息时优先使用其目标 Operator；回答、Ask/Confirm
等发给用户的消息则使用发送方 Operator，自定义角色归属到其 owning Operator。
重新打开会话默认选择最新消息，因此只有用户输入、尚无回答时也能观察工作过程。
仅改变发送框中的目标不切换正在查看的详情；切换 User conv 时清除上一会话的详情。
没有关联 Operator 的消息不猜测归属。页面连接在线或等待工作会话不代表 Operator 已消费消息。

> 待补充：面板切换、尺寸与响应式布局、空态，以及选中状态的恢复规则。

## 消息呈现

消息按身份独立展示与更新，不依赖输入和输出交替。页面允许人在 Operator 工作期间继续发言，
也允许 Operator 多次回应；流式控制事件不生成空消息气泡。

处理详情按相交时间区间分组，同组按 Actor 分列。与前组无交集的时间段重新靠左，
自定义 `operator/<operator-key>/<role>` 作者显示角色名和完整身份，选中主会话中的角色消息时
按 owning Operator 定位共享内部会话。report 的 title、actor_display_name 和错误供详情展示。

卡片按内容自然高度展示，不按持续时长拉伸。区间代表可见活动，不是精确执行耗时或因果关系。

> 待补充：内容块与工具展示、Actor 颜色与步骤标识、滚动跟随，以及消息引用的交互规则。

## Ask/Confirm 卡片

卡片是问题 Message 的交互呈现，用户操作通过精确回复引用返回类型化结果。用户也可以继续
发送普通消息，但这不自动替代卡片答复或表示批准。卡片状态以 server 保存的结果为准，
不随页面流关闭而结束；调用与状态契约见 [Runtime](../../docs/runtime.md#humanask-与-confirm)。

问题和答复各自保留一条消息及一张卡片。Ask 以单选项和可选自由输入展示；Confirm 使用同意、
拒绝及其自定义标签。用户选择后，问题卡片与答复卡片均保留选项、选中态和可读标签，进入只读。
自由回答保留原文。取消、超时和失败保留原问题内容及状态，不显示虚构选择；超时不生成答复。
提交期间禁用操作，失败后保留输入并允许重试；Escape 与“忽略 / 取消”产生 dismissed 答复。
拒绝 Confirm 是选择 declined，与取消具有不同语义。

### 自包含的消息呈现

每条 Message 的 `content` 保存自身渲染所需的数据。正式答复通过身份、选项和状态校验后，
server 在同一事务内更新问题 block 的 `selected_value`，并在答复的 `human_reply` block 中
保存 `question` 快照：题目、选项及标签、交互状态和选中值。快照来自已校验的问题，不信任
客户端提交的展示数据；取消不产生选中值。答复保留接受当时的快照，不追随原问题后续变化。

分页历史、Human 响应与 SSE 直接交付相同的消息内容，不查询其他消息补齐卡片，也不附加
`card` 或 `reply_to` 展示对象。问题页不需要加载答复，答复页不需要加载原问题；分页的
消息集合、顺序和条数保持不变。物理 Part 的内容展开仍由 repo 负责。

前端按本条消息中的 Ask/Confirm 或 Human reply block 生成问题或只读答复卡片。
普通发言即使带有引用或类似 Ask/Confirm 的内容，也不会成为正式交互。AgentUE 普通 blocks
继续按各自类型展示：text 保留原文，markdown 渲染 Markdown，tool 展示工具信息。
`reply_to_id` 保留关联与“查看所回复的消息”跳转，不为引用加载预览。

答复响应同时返回更新后的问题和新答复，页面按消息 ID/revision 合并。主会话与右侧详情
共用卡片组件；页面只负责本地渲染和提交状态，正式结果仍由 server 决定。

DB 合并快照与 Redis append-only 事件流的存储区别、写入顺序和恢复边界，统一见
[持久化约定](persistence.md#agentue-快照与实时事件流)。

## 页面交付

task_id 是输入提交的交付标识，不是 Operator 的业务任务；server 不建立 tasks 表。
页面订阅以 Conv 寻址，Redis 事件流以 Message 寻址。

### 提交与观察

用户提交只创建真实 Message，不预建空回答。Operator/Harness 发言、
Ask/Confirm 在实际发生时各自创建 Message。人可以连续追加，Operator 可以多次回应，
输入与输出数量没有一对一约束。

消息提交只依赖 DB，Redis 不进入输入事务。DB 接收后，即使页面桥暂时不可用，也不要求用户
重新发送。Conv 通知用同事务保存的待通知标记在提交后重试；消费契约见 [Conversation](conversation.md)。
创建接口 `POST /v1/conversations/:conversation_id/messages` 是短请求，可带初始或完整正文，
返回已接受的消息元信息后结束 JSON 响应。Speak 一次完成，Tell 以 streaming 状态创建，
后续通过 `POST /v1/messages/:message_id/events` 写入 set/append/end。
Server 不为两个 runtime Verb 区分 HTTP 入口。观察页面使用
`GET /v1/conversations/:conversation_id/stream`，不需要先发送消息，也不需要 task_id。
主对话和当前右侧详情分别订阅自身 Conv，不隐式订阅所有子会话；再次发言不替换订阅。
HTTP/SSE 断开不取消执行。

Operator 通过 Poll 接收消息，通过 Read/List 选择消息并按需读正文；不提供按 task_id 配对输入与回答的业务入口。

### 消息寻址与快照

每条 Message 有独立的 AgentUE model、block ID 与 seq/revision。
Conv.Speak 原子发布完整消息并返回只读 Message；Conv.Tell 创建开放消息，
返回的 Stream 句柄通过 Emit/End 按 Message ID 更新消息；Operator 不传 task_id。
Human 问题与答复由 typed Verb 管理，普通流式写入不能伪造批准。

MessageService 统一承接消息创建和 EmitMessage：前者创建消息，后者先原子推进 SQL 可见快照，再尽力写 Redis。DB 接收即发送成功；桥故障只影响页面
实时性，不改变 Actor 的协作结果。runtime 隐藏序号与瞬时重试，SQL 保存最后事件指纹，避免
响应丢失后重复追加；同一序号不同内容会冲突。Message End 不代表 Actor 或整个 Conv 完成。

所有输出都通过 Message 寻址。订阅持续检查 SQL Revision，用完整快照修复未送达的增量；
桥连续时交付增量，发生版本缺口或乱序时发送最新快照。页面发现晚到的发言和内容，
不依赖写入时命中了哪个 server 实例。

### 聚合流与恢复

页面先分页发现消息元信息，再有界并发获取逻辑正文；列表不加载完整内容。
每次 stream 请求创建一个 Conv Listener，聚合当前运行态消息的独立 Redis 流。
AgentUE 的 `stream_id` 在这里取 Message ID，客户端先按它分流，再按各自的 seq/revision 合并。
这是一条 SSE 连接上的逻辑多路复用，不是共享内容模型；AgentUE 本身不要求所有使用方提供
`stream_id`。seq 和 Redis cursor 只在单条消息内有意义，不充当共享 Conv 游标。
连接的 ping 不带 stream_id，也不创建消息气泡。

首次发现、重连恢复和状态变化时，快照以 `{message, event}` 交付完整消息及带 stream_id 的
AgentUE Start。普通增量直接交付带 stream_id 的 AgentUE event，不附带 Message 或既有正文；
页面保留已知的身份、引用和状态，只合并本次内容变化。终态通过快照同步 Message.status，
随后发送该消息的 End，不关闭其他消息流。引用卡片的展示数据已自包含，逐帧路径不查询被回复消息。

server 按 ID 定期增量发现该 Conv 的新消息。新的一次性发言直接交付，流式发言加入监听；
状态检查只读取运行态消息的元数据，revision 变化或增量缺口才加载正文快照，不重复扫描终态历史。
Ask/Confirm 已发送的卡片仍可能待答，因此其交互状态独立观察。

一条 Message 的 end 移除自身监听，不关闭 Conv 连接。切换会话或离开页面主动取消连接；
断线退避重连，从 SQL 恢复运行态快照再接 Redis。每次连接建立后，页面做一次有界的活跃消息
revision 查询及新增消息查询，补偿首次加载的时间差和断线期间的变化，避免刚结束的消息被遗漏。
连接期间的增量发现和状态校验由 Listener 承担；浏览器不另开常驻消息轮询。
Listener 随请求取消，不放入全局注册表，也不负责消息 GC。

任一 server 实例都可观察同一 Conv。Redis 丢失后，已接受的内容可以从 SQL 快照恢复，
但不会重新生成每个中间增量；AgentUE Bridge 负责事件协议和续接，server 负责消息寻址与快照。

### 消息结束与重试


默认 Speak 在创建事务中保存完整正文与结束状态；非流式错误发言可指定 status=failed，
同时在 AgentUE meta.error 中提供展示内容，无需为了保存错误而开启消息流。流式 End 与内容事件使用同一顺序和重试契约：
先原子推进 SQL Revision 与 Message.status，再尽力更新消息桥并标记终态。
SQL 失败由句柄重试原事件，不另分配 seq；重复 End 幂等。
普通 Speak 的内容和 Emit 不能更改消息终态；写入者通过 End 结束流式消息，server 还会收口长期失活的输出。

Message.status 表达单条消息的发送生命周期，独立于 AgentUE 内容：streaming 表示仍在输出，
completed 表示发送完成，failed 表示输出失败，cancelled 表示输出被取消，expired 表示长期未更新。
End 默认 completed，也可显式传入 failed/cancelled；不同终态不能互相覆盖。
更新请求将 status 与 AgentUE event 并列传入，AgentUE End 本身不携带状态；页面事件的 Message
外层与历史 API 都返回持久化 status。Redis 丢失后也不会把已结束消息重新视为正在输出。
主对话和详情只对 streaming 消息显示“生成中”，不把“连接在线”误标为“Operator 正在执行”。
Ask/Confirm 卡片发送完成即 completed，但交互仍可等待答复；两种生命周期互不替代。

没有 Delivery.Complete 或输入关闭意图。页面拥有订阅生命周期；
Operator 只表达自己何时说完一条消息。End 不删除 Conv、不自动 Commit、不终止待答问题，
也不禁止任何 Actor 用新 Key 再次发言。

### 失活与 TTL

`MESSAGE_TTL` 统一配置输出失活期限与 Redis 事件保留期限，默认 24h。
Message GC 随 server 启停，即使没有页面连接也独立执行有界清理。
它按 DB 的 `updated_at + TTL` 定期将普通 streaming 消息标记为 expired，并递增 revision；
Harness Run 的输出由 Run deadline 和租约驱动者收口，不参加普通 Message GC，避免仍活跃的执行失去结果写入入口。
无需 expires_at 列。保留最后正文与最后活动时间，页面展示“已过期”，迟到写入不能恢复该消息。

Redis 按自己的写入时间续期，DB 按 server 实际接受输出的时间续期；读取、心跳和重复事件
不延长 DB 生命周期。两层允许短暂不一致，不根据 Redis key 是否存在推断消息状态。
过期只结束页面消息，不取消 Harness 执行、不代替 Operator 判断业务完成；新发言使用新消息。
