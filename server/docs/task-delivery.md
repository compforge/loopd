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

Operator 和内部 Harness 通过 loop-runtime 向任意 server 实例发布 AgentUE `set/append`，server
写入共享 Redis Stream。页面可按 task_id 连接任意实例观察，HTTP/SSE 连接不拥有执行生命周期。

Task Stream 同时传输主回答与详情消息的可见事件，不等于主回答 Message 的 content。server 接受
Harness 的首批输出时，为主回答建立详情 Conversation 和 Harness Message；重复交付复用身份。
页面先查询真实子会话及其 Message，再按 Harness 身份叠加 Task Stream 中的流式输出。
详情列表只在选中的任务运行时轮询；完成与刷新后读取数据库快照。

首次请求与续接使用同一个 Chat API：不带 task_id 创建问答，带 task_id 观察已有问答。AgentUE
delivery 重建完整 `start` 快照后继续输出，API 不单设 replay 资源。

公开 Event 的 ID 是 Redis/SSE 传输标识，用于 `Last-Event-ID`。Data 内的 AgentUE seq 表达语义
顺序与发布幂等，二者不能互相替代。发布方的 seq 分配与恢复边界见 [Runtime](../../docs/runtime.md)。

Human Message 在数据库事务提交后即可交付：SSE 外层使用 `{message_id, message?, event}`，
内层仍是标准 AgentUE event。主回答保持 Redis seq；每条 Human Message 使用自己的 revision
发布 start 快照，Task Stream 定期聚合变化。重连先读取持久 Human 快照，再续接主回答，结束前
补齐 Human 更新。Human 快照不占用 Redis cursor，不推进 Last-Event-ID，也不混入主回答。
客户端按 message_id 应用快照，block ID 只在该 Message 内唯一。

Redis 丢失后 Human 内容、终态与待交付通知仍由数据库恢复；主回答只能恢复最后持久快照，
尚未固化的 Harness/主回答 delta 仍可能丢失。已关闭 Task 可直接从持久主回答发送 start/end。

AgentUE 拥有事件协议、Reducer、Bridge 和续接；server 管理 Redis client 生命周期和最终 Message。
内置内存 Redis 无法承诺重启后保留活跃流，持久聊天由数据库保存，完整执行轨迹由 AgentLedger 承载。

## 完成与重试

完成前先锁定主 response，拒绝未答问题上的正常收口；失败收口将剩余请求标成 failure(task_ended)。
在事务中保存收口意图并关闭创建／答复入口，随后固化 Message 快照、终结 Stream、再删除 Task。Task 一旦删除，页面回答已经可恢复，
Operator 重启也不会因旧 Task 再次执行已完成问答。

固化时按 Harness 身份将文本和工具块合并到详情 Message，并将其余内容写入主回答。
详情和主回答全部写入成功后才能终结 Stream；部分写入失败时，用同一批 Message ID 重试。

删除 Task 失败必须返回可重试错误；重复完成不重复写入回答。runtime 的 Task watch 不把完成后
的删除事件当作新工作，避免持久 Message 被再次解释为待处理请求。

主 response 的收口意图保留到 Task 删除成功。server.Run 重试中断的交付与删除，完成后标记
closed；重复提交同一完成意图可恢复，改变完成意图返回冲突。Human 到期、答复与收口共用主
response 行锁，通知重试只更新现有 Task revision，绝不重建已删除的 Task。
