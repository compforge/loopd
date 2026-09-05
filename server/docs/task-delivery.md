# Task 与流式交付

Task 是一次问答的持久路由与唤醒入口，server 不再建立同义的 tasks 表。数据库拥有可见聊天，
Kubernetes 拥有 Task，Redis 承载活跃问答的页面事件；三者协作的边界由 ChatService 与 delivery 维护。

## 创建与提交

首次 Chat 请求在数据库事务中创建 user Message 和目标 Operator/Harness 的空 response Message，
两条记录共享 task_id。提交事务前，server 以同一 ID 初始化 AgentUE Stream，再创建 Task CRD。

数据库事务不覆盖 Redis 和 Kubernetes。任一步失败时回滚 Message，并尽力删除已创建的 Stream
与 CRD；外部资源已创建但数据库提交失败时，也执行同样补偿。该流程没有分布式原子提交保证。

Task 可能先于数据库提交被 Operator 观察到。此时 `Task.Get` 返回 not found，Reconciler 应重试。
Task 的通用字段遵循 Kubernetes API 兼容规则；生成物更新要求见根目录 AGENTS.md。

## 上下文边界

server 根据 task_id 从主会话的 Message 即时组装 Conversation、当前 input、response 和截至
input 的有界 History。返回的历史水位和截断标志用于显式读取更早的消息，不把完整聊天复制进 CRD。

详情会话可以复用 task_id，但其消息不得覆盖主链路 input/response。Human 反问、确认等交互应沿用
一次问答的 Task identity；当前 runtime 尚未提供专门的 Ask/Confirm API。

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
顺序与发布幂等，二者不能互相替代。同一 runtime 对同一 Task 的并行输出串行分配 seq；这不是
多个独立 runtime 之间的全局分配器。稳定执行恢复仍由 Harness 及其 Adapter 承担。

AgentUE 拥有事件协议、Reducer、Bridge 和续接；server 管理 Redis client 生命周期和最终 Message。
内置内存 Redis 无法承诺重启后保留活跃流，持久聊天由数据库保存，完整执行轨迹由 AgentLedger 承载。

## 完成与重试

完成顺序是固化 Message 快照、终结 Stream、再删除 Task。Task 一旦删除，页面回答已经可恢复，
Operator 重启也不会因旧 Task 再次执行已完成问答。

固化时按 Harness 身份将文本和工具块合并到详情 Message，并将其余内容写入主回答。
详情和主回答全部写入成功后才能终结 Stream；部分写入失败时，用同一批 Message ID 重试。

删除 Task 失败必须返回可重试错误；重复完成不重复写入回答。runtime 的 Task watch 不把完成后
的删除事件当作新工作，避免持久 Message 被再次解释为待处理请求。
