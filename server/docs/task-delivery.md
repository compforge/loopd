# Chat 与流式交付

`task_id` 标识一次 Chat 的页面交付，不是 Operator 的业务任务。数据库保存可见 Message，
Conv CRD 保存定向通知与 Listen 游标，Redis 承载活跃页面事件；server 不建立 tasks 表。

## 创建与通知

首次 Chat 只创建带收件者的 user input Message，并初始化对应 Redis 流，不预建回答。
回答、Ask/Confirm 与工作输出在参与者实际发起时创建独立 Message。

Redis 初始化位于输入提交边界内：失败回滚输入；数据库提交失败时尽力删除已初始化的流。
这不是跨系统事务，进程崩溃可能留下无对应输入的临时流。

定向通知在数据库提交后更新 Conv CRD；输入的待通知标记与 Message 同事务保存，通知失败由
server 后台重试，不要求用户再次发送。具体接收契约见 [Conversation](conversation.md)。

## 上下文与发布

`Chat.Context` 按交付 ID 解析原始 input、收件者及截至 input 的有界 History；response 可不存在。
`Chat.Messages` 读取该交付的全部可见消息；`Conv.Read` 读取会话历史；
`Conv.Listen` 接收参与者的新消息。它们都不需要 Task CRD。

每条输出 Message 有独立 AgentUE model、block ID 和 seq。调用者可以先 `Chat.Output` 建立明确
地址，再 `EmitMessage` 发布；指定 User conv 可在主会话发布，省略则使用处理详情。
`Chat.Emit` 是单条默认回答的便捷入口，在首次有效发布时创建回答，不能依赖它预先存在。
同一输出应选择一种发布入口，默认 Emit 和显式 EmitMessage 的本地 seq 分配不混用。

Human 请求与答复仍通过 typed Verb 修改。明确回复引用不能以“最近一条消息”推断，
普通发言也不自动成为 Ask/Confirm 的返回值。

## 聚合流与 replay

- 有消息身份的事件使用 `{message_id, message, event}` 外层；所有参与者共享客户端 Message 更新契约。
- 每条真实 Message 使用独立 Redis 流，包括默认回答。Human 消息按 revision 发布数据库快照。
- 不带消息身份的 Start/Error/End 是 Chat 控制事件，只管理交付生命周期，不创建页面气泡。
- Last-Event-ID 只表示 Chat 控制流的位置；各 Message 在重连时独立重放，客户端按 ID/revision 合并。
- 任一 Message 的 end 都不结束 Chat。全部输出和 Human 状态交付后，才发送 Chat end。
- 同一 Conversation 可同时观察多个 task_id；完成一个不能移除其他流的 replay 记录。

不带 task_id 的 Chat 请求创建输入，带 task_id 的请求只观察，不再创建输入或通知 Operator。
HTTP/SSE 只负责观察，断开不取消执行。已关闭交付从数据库恢复消息和失败信息，不依赖 Redis
或 CRD 存活；尚未固化的 delta 不承诺在 Redis 丢失后恢复。

## 完成与重试

输入 Message 的行锁串行化交互写入与交付收尾；它是已经存在的聊天事实，不是空回答或领域任务。
正常收尾拒绝未答 Human 问题；失败收尾将剩余问题标为 failure。事务保存关闭意图并停止追加，
随后逐条固化输出、终结 Redis 控制流，最后标记输入交付 closed。完成交付不删除 Conv CRD。

相同完成意图可重试，变化冲突。中断后的完成重试由 Chat 生命周期拥有，不归 Human 管；
Human 只推进问题 deadline，卡片答复由定向 Message 通知，等待者也可轮询权威状态。

Operator 可以将多次 Chat 发言合并成一轮业务执行。Router 将一条最终回答发布到初始 Chat，
并结束本轮接收的所有 Chat 流；这些交付 ID 不要求各自拥有一条回答。

AgentUE 拥有事件协议、Reducer、Bridge 和单流续接；server 拥有消息寻址、聚合传输与交付收尾。
