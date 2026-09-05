# Task 与流式交付

Task 是一次问答的持久路由与唤醒入口，server 不再建立同义的 tasks 表。数据库拥有可见聊天，
Kubernetes 拥有 Task，Redis 承载活跃问答的页面事件；三者协作的边界由 ChatService 与 delivery 维护。

## 创建与提交

首次 Chat 请求在数据库事务中创建 user Message 和目标 Operator/Harness 的空 response Message，
两条记录共享 task_id。提交事务前，server 以同一 ID 初始化 AgentUE Stream，再创建 Task CRD。

数据库事务不覆盖 Redis 和 Kubernetes。任一步失败时回滚 Message，并尽力删除已创建的 Stream
与 CRD；外部资源已创建但数据库提交失败时，也执行同样补偿。该流程没有分布式原子提交保证。

Task 可能先于数据库提交被 Operator 观察到，此时上下文查询暂时返回 not found。调用方如何重试
以及 Task watch 的语义见 [Runtime](../../docs/runtime.md)。
Task 的通用字段遵循 Kubernetes API 兼容规则；生成物更新要求见根目录 AGENTS.md。

## 上下文边界

server 根据 task_id 从主会话的 Message 即时组装 Conversation、当前 input、response 和截至
input 的有界 History。返回的历史水位和截断标志用于显式读取更早的消息，不把完整聊天复制进 CRD。

详情会话可以复用 task_id，但其消息不得覆盖主链路 input/response。Human 交互的开发能力与边界
统一见 [Runtime](../../docs/runtime.md)。提问和答复按 Message 回复关系关联，
主 input/response 由创建事务的 purpose 标记确定，不从后到的交互消息重新选择。
答复必须严格按 reply_to_message_id 定位问题，不以消息邻接或“最近的问题”兜底。回复关系
本身不区分反问和主回答，不能用它代替主链路身份。

## 发布与续接

每条输出 Message 有独立的 AgentUE model、block ID 空间与 seq。runtime 先通过 Output
创建或复用工作 Message，再按 message_id 发布 set/append；server 校验它属于指定 Task，
不从 block 内容推断消息身份。Human 请求与答复仍必须通过 typed Write Effect 修改。

Task Stream 只聚合传输，SSE 外层为 `{message_id, message?, event}`，内层保持标准 AgentUE event。
页面对所有参与者使用同一个按 Message 应用事件的入口，不把多个模型合并成主回答。

- 主回答和工作输出各自复用 AgentUE Redis Bridge / Replayer，完成时固化各自快照。
- Human 交互在数据库提交后即可观察；每条消息按自己的 revision 发布 start 快照。
- Last-Event-ID 续接主回答的传输位置；工作输出在每次连接中独立重放，Human 重发权威快照。
  因此非主回答事件不推进该游标，客户端必须按 Message ID 隔离状态。
- 工作消息的 end 只结束该消息。聚合交付补齐全部输出与 Human 更新后，才发送主回答 end。
- HTTP/SSE 连接只负责观察；断开连接不取消 Task 或任何 Effect。

首次请求与续接使用同一个 Chat API：不带 task_id 创建工作，带 task_id 观察已有工作。
Redis 丢失后，只能恢复各消息最后持久的快照，尚未固化的输出 delta 仍可能丢失。
已关闭 Task 从数据库重放所有可见消息，再发送结束标记，不依赖 Redis 或 CRD 存活。

AgentUE 拥有事件协议、Reducer、Bridge 和单消息续接；server 拥有消息寻址、聚合交付与任务收尾。
完整执行轨迹由 AgentLedger 承载，页面事件不能替代它。

## 完成与重试

完成前先锁定主 response，拒绝未答问题上的正常收口；失败收口将剩余请求标成 failure(task_ended)。
在事务中保存收口意图并关闭创建／答复入口，随后固化 Message 快照、终结 Stream、再删除 Task。Task 一旦删除，页面回答已经可恢复，
Operator 重启也不会因旧 Task 再次执行已完成问答。

各 Message 从自己的事件流固化快照，工作输出先于主回答完成；部分写入失败时用相同身份重试。
Message 的完成不自动删除 Task，只有整体交付完成才允许退休 Task。

删除 Task 失败必须返回可重试错误；重复完成不重复写入回答。runtime 的 Task watch 不把完成后
的删除事件当作新工作，避免持久 Message 被再次解释为待处理请求。

主 response 的收口意图保留到 Task 删除成功。server.Run 重试中断的交付与删除，完成后标记
closed；通用恢复由 Chat 生命周期拥有，不依赖是否创建过 Human 问题。
Human 生命周期只负责问题到期与通知重试。重复提交同一完成意图可恢复，改变完成意图返回冲突。Human 到期、答复与收口共用主
response 行锁，通知重试只更新现有 Task revision，绝不重建已删除的 Task。
