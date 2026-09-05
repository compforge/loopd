# Message 与页面交付

task_id 标识一次页面交付及 Redis 流，不是 Operator 的业务任务。数据库保存可见 Message，
Conv CRD 保存消费位置与通知，Redis 承载活跃事件。server 不建立 tasks 表。

## 提交与观察

用户提交只创建真实 Message 和对应页面流，不预建空回答。Operator/Harness 发言、
Ask/Confirm 在实际发生时各自创建 Message。人可以连续追加，Operator 可以多次回应，
输入与输出数量没有一对一约束。

Redis 初始化在输入提交边界内：失败回滚输入；数据库提交失败尽力删除已初始化流。
这不是跨系统事务，崩溃可能留下临时孤立流。Conv 通知用同事务保存的待通知标记在提交后重试，
不要求用户重复发送。消费契约见 [Conversation](conversation.md)。

带 task_id 的请求只观察既有交付，不创建输入或通知。HTTP/SSE 断开不取消执行。
Conv.Context 提供消息级上下文；不提供按 task_id 配对输入与回答的业务入口。

## 消息寻址与快照

每条 Message 有独立的 AgentUE model、block ID、seq/revision 与 Redis 流。
Conv.Speak 建立消息地址，随后按 Message ID 写事件；TaskID 可选，不限制后续发言。
Human 问题与答复由 typed Verb 管理，普通流式写入不能伪造批准。

会话级消息事件先写 Redis，再推进 SQL 可见快照。SQL 失败时以相同 seq 和内容重试，
不分配新 seq；同一 Message 的写入者负责保持有序。Message end 固化并终结该消息流，
不代表 Actor 或整个 Conv 完成。

所有输出都通过 Message 寻址。页面会持续刷新会话消息，发现晚到的发言和内容，
不依赖某个 task_id 的观察连接仍然打开。

## 聚合流与 replay

- 有消息身份的事件用 message_id、message、event 外层寻址，客户端按 ID/revision 合并。
- 没有消息身份的 start/error/end 是 UI 控制事件，不创建气泡。
- Last-Event-ID 是聚合控制流位置；各 Message 在重连时独立重放。
- 一条 Message 的 end 不关闭 UI 流；不同 task_id 的观察互不替代。
- 已关闭交付从 SQL 恢复当时及已持久化的相关消息；后续独立发言仍由会话列表呈现。

任一 server 实例都可观察同一交付，不依赖发布时命中的实例。Redis 丢失不能恢复尚未持久化的
增量；AgentUE Bridge 负责事件协议、幂等与续接，server 负责消息寻址和 SQL 快照。

## 完成与重试

输入 Message 保存 UI 关闭意图，行锁只协调该交付收尾，不作为业务任务锁。
server 终结控制流并标记 closed，观察方在送达 end 前刷新相关消息的当前快照；相同意图可重试，变化冲突。
中断后的恢复由通用交付维护循环承担，不归 Human 生命周期所有。

关闭交付不删除 Conv、不自动 Commit 消费位置、不终止待答问题，也不禁止 Actor 再次发言。
Human 问题依赖自己的期限、身份和不可变终态。Operator 决定何时关闭当前页面观察，以及何时
完成自己的业务工作，两者不能相互推断。
