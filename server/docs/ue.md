# 用户交互与页面交付

本文描述参与者协作如何呈现在页面上：布局、消息呈现、Ask/Confirm 卡片，以及支撑这些体验的
流式交付、快照、重连与收尾。业务消费协议与存储事实分别由 Conversation、Persistence 文档拥有。

参与者之间的协作依赖持久消息与 [Poll/Commit](conversation.md)，不依赖页面连接。
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

Redis 初始化在输入提交边界内：失败回滚输入；数据库提交失败尽力删除已初始化流。
这不是跨系统事务，崩溃可能留下临时孤立流。Conv 通知用同事务保存的待通知标记在提交后重试，
不要求用户重复发送。消费契约见 [Conversation](conversation.md)。

带 task_id 的请求只观察既有交付，不创建输入或通知。HTTP/SSE 断开不取消执行。
Conv.Context 提供消息级上下文；不提供按 task_id 配对输入与回答的业务入口。

### 消息寻址与快照

每条 Message 有独立的 AgentUE model、block ID、seq/revision 与 Redis 流。
Conv.Speak 建立消息地址，随后按 Message ID 写事件；TaskID 可选，不限制后续发言。
Human 问题与答复由 typed Verb 管理，普通流式写入不能伪造批准。

会话级消息事件先写 Redis，再推进 SQL 可见快照。SQL 失败时以相同 seq 和内容重试，
不分配新 seq；同一 Message 的写入者负责保持有序。Message end 固化并终结该消息流，
不代表 Actor 或整个 Conv 完成。

所有输出都通过 Message 寻址。页面会持续刷新会话消息，发现晚到的发言和内容，
不依赖某个 task_id 的观察连接仍然打开。

### 聚合流与 replay

- 有消息身份的事件用 message_id、message、event 外层寻址，客户端按 ID/revision 合并。
- 没有消息身份的 start/error/end 是 UI 控制事件，不创建气泡。
- Last-Event-ID 是聚合控制流位置；各 Message 在重连时独立重放。
- 一条 Message 的 end 不关闭 UI 流；不同 task_id 的观察互不替代。
- 已关闭交付从 SQL 恢复当时及已持久化的相关消息；后续独立发言仍由会话列表呈现。

任一 server 实例都可观察同一交付，不依赖发布时命中的实例。Redis 丢失不能恢复尚未持久化的
增量；AgentUE Bridge 负责事件协议、幂等与续接，server 负责消息寻址和 SQL 快照。

### 完成与重试

输入 Message 保存 UI 关闭意图，行锁只协调该交付收尾，不作为业务任务锁。
server 终结控制流并标记 closed，观察方在送达 end 前刷新相关消息的当前快照；相同意图可重试，变化冲突。
中断后的恢复由通用交付维护循环承担，不归 Human 生命周期所有。

关闭交付不删除 Conv、不自动 Commit 消费位置、不终止待答问题，也不禁止 Actor 再次发言。
Human 问题依赖自己的期限、身份和不可变终态。Operator 决定何时关闭当前页面观察，以及何时
完成自己的业务工作，两者不能相互推断。
