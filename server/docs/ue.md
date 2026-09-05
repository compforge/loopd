# 用户交互与页面交付

本文描述 Actor 协作如何呈现在页面上：布局、消息呈现、Ask/Confirm 卡片，以及支撑这些体验的
流式交付、快照、重连与收尾。业务消费协议与存储事实分别由 Conversation、Persistence 文档拥有。

Actor 之间的协作依赖持久消息与 [Poll/Commit](conversation.md)，不依赖页面连接。
Redis 的 replay 位置不代表 Operator 已经消费，Operator 的 Commit 也不代表页面已经收到输出。
DB、CRD 与 Redis 的整体责任分层见 [Kernel](../../docs/kernel.md)。

## 页面布局

页面按左、中、右组织：左侧列出 User conv，中间展示选中会话的消息与发送框，右侧展示
选中消息对应 Actor 的内部会话。发送目标跟随发送框，每次发言可以选择不同参与者。

> 待补充：面板切换、尺寸与响应式布局、空态，以及选中状态的恢复规则。

## 消息呈现

消息按身份独立展示与更新，不依赖输入和输出交替。页面允许人在 Operator 工作期间继续发言，
也允许 Operator 多次回应；流式控制事件不生成空消息气泡。

处理详情按相交时间区间分组，同组按 Actor 分列。与前组无交集的时间段重新靠左，
卡片按内容自然高度展示，不按持续时长拉伸。区间代表可见活动，不是精确执行耗时或因果关系。

> 待补充：内容块与工具展示、Actor 颜色与步骤标识、滚动跟随，以及消息引用的交互规则。

## Ask/Confirm 卡片

卡片是问题 Message 的交互呈现，用户操作通过精确回复引用返回类型化结果。用户也可以继续
发送普通消息，但这不自动替代卡片答复或表示批准。卡片状态以 server 保存的结果为准，
不随页面流关闭而结束；调用与状态契约见 [Runtime](../../docs/runtime.md#humanask-与-confirm)。

> 待补充：选项、自由输入、确认与取消的呈现，以及提交中、已答复、超时和失败状态的交互。

## 页面交付

task_id 标识一次页面交付及 Redis 流，不是 Operator 的业务任务，server 不建立 tasks 表。

### 提交与观察

用户提交只创建真实 Message 和对应页面流，不预建空回答。Operator/Harness 发言、
Ask/Confirm 在实际发生时各自创建 Message。人可以连续追加，Operator 可以多次回应，
输入与输出数量没有一对一约束。

消息提交只依赖 DB，Redis 不进入输入事务。DB 接收后，即使页面桥暂时不可用，也不要求用户
重新发送。Conv 通知用同事务保存的待通知标记在提交后重试；消费契约见 [Conversation](conversation.md)。
首次提交在连接页面桥前返回已接受的消息与 task_id；随后断线按该身份重连，不另建输入。

带 task_id 的请求只观察其所属会话，不创建输入或通知。一个页面订阅覆盖 User conv 及其直接
内部会话，包括不同 task_id 或无 task_id 的后续发言。HTTP/SSE 断开不取消执行。
Operator 通过 Poll 接收消息、Read 读取历史；不提供按 task_id 配对输入与回答的业务入口。

### 消息寻址与快照

每条 Message 有独立的 AgentUE model、block ID 与 seq/revision。
Conv.Speak 默认原子发布完整消息，不创建独立消息流；只有 Stream=true 时保持开放，
返回的句柄通过 Emit/End 按 Message ID 更新消息；Operator 不传 task_id。
Human 问题与答复由 typed Verb 管理，普通流式写入不能伪造批准。

消息事件先原子推进 SQL 可见快照，再尽力写 Redis。DB 接收即发送成功；桥故障只影响页面
实时性，不改变 Actor 的协作结果。runtime 隐藏序号与瞬时重试，SQL 保存最后事件指纹，避免
响应丢失后重复追加；同一序号不同内容会冲突。Message End 不代表 Actor 或整个 Conv 完成。

所有输出都通过 Message 寻址。订阅持续检查 SQL Revision，用完整快照修复未送达的增量；
桥连续时交付增量，发生版本缺口或乱序时发送最新快照。页面发现晚到的发言和内容，
不依赖写入时命中了哪个 server 实例。

### 聚合流与 replay

- 有消息身份的事件用 message_id、message、event 外层寻址，客户端按 ID/revision 合并。
- 没有消息身份的 start/ping 是 UI 连接控制事件，不创建气泡，也没有业务结束信号。
- Last-Event-ID 是聚合控制流位置；各 Message 在重连时独立重放。
- 一条 Message 的 end 不关闭 UI 流；已结束消息和 Human 卡片以 SQL 快照交付。
- 页面仅保留所选会话的一条订阅；再次发言替换连接身份，但仍可收到旧输入对应的后续输出。
- 切换会话／离开页面主动取消连接，正常 EOF 或断线按保存的 task_id 退避重连。

任一 server 实例都可观察同一交付。Redis 丢失后，已接受的内容可以从 SQL 快照恢复，
但不会重新生成每个中间增量；AgentUE Bridge 负责事件协议和续接，server 负责消息寻址与快照。

### 消息结束与重试

默认 Speak 在创建事务中保存完整正文与结束状态。流式 End 与内容事件使用同一顺序和重试契约：
先原子推进 SQL Revision 与受控 meta.output.ended=true，再尽力更新消息桥并标记终态。
SQL 失败由句柄重试原事件，不另分配 seq；重复 End 幂等。
普通 Speak 的内容和 Emit 不能伪造受控 meta.output；客户端收到 End 也将同一消息标为已结束。

AgentUE 原生 reducer 的 End 不改变 model，因此 loopd 将消息结束状态额外保存在可见快照的 meta
中，不新增生命周期表。Redis 丢失后已固化的结束状态仍可恢复，不会把结束消息重新视为正在输出。
页面只对仍开放的 output 显示 STREAMING，不把“连接在线”误标为“Operator 正在执行”。

没有 Delivery.Complete、输入关闭意图或通用页面收尾维护循环。页面拥有订阅生命周期；
Operator 只表达自己何时说完一条消息。End 不删除 Conv、不自动 Commit、不终止待答问题，
也不禁止任何 Actor 用新 Key 再次发言。
