# loop-server Persistence

本文说明 loop-server v1 的数据库对象和恢复语义。它是 server 模块实现文档，不定义 Operator 的
领域模型；稳定产品边界见 [`../../docs/kernel.md`](../../docs/kernel.md)。

## 表与 owner

loop-server 使用名为 `loopd` 的逻辑数据库。v1 默认 SQLite，存储模型刻意限制在通用协作事实：

| 表 | 内容 |
|---|---|
| `conversations` | 持久会话、标题和当前消息 sequence |
| `messages` | Conversation 内按 sequence 排序的 `user/harness/operator` 消息 |
| `invocations` | 一条用户 Message 交给一个 responder 处理的生命周期，以及可选 Operator Resource 引用 |
| `activities` | Invocation 详情中的通用、可覆盖进度项 |
| `harness_calls` | `owner_uid + effect_key` 唯一的 Harness 外部执行、当前观测和结果 |
| `interactions` | Ask/Confirm 的待决问题、选择、回答和终态 |
| `invocation_events` | 面向 AgentUE 和 Call observer 的持久递增 cursor |

数据库不包含 Operator、Task、Manager、Executor、Auditor 等领域表。Operator 的 CRD/status 才是领域
Loop 的事实来源；`invocations.resource_*` 只保存反向定位所需的引用。

agentledger 后续可以消费这些状态变化，记录不可变审计和成本事实，但不参与请求处理事务，也不作为
恢复 Conversation 或 Interaction 的来源。

AgentUE Runner 当前依赖 Python/Redis 来拥有后台任务和 heartbeat recovery。loop-server 不再为同一个
Invocation 引入第二套执行账本，只使用 AgentUE UI protocol 与 SSE binding；Python Harness 或 Operator
可在自己的进程边界内使用 AgentUE Runner。

## 长生命周期

Invocation 可能运行数分钟到数天，因此：

- 创建用户 Message 与 Invocation 在同一事务中完成；
- Harness Call 在触发 provider 之前获得持久 ID 和幂等 key；
- loop-server 仅执行有显式 timeout 的 `Ensure` 或 `Observe`，不把 provider 的完整执行绑在 goroutine、
  HTTP request 或进程生命周期上；
- 重启后 Runner 扫描非终态 Call，以同一 key 重新观察或恢复；
- provider cursor 与 loopd event cursor 分开保存；重读上游流时用 provider cursor 去重，UI 与 Operator
  观察则只使用 loopd cursor；
- Operator 自己通过 CRD/Reconcile 恢复，不依赖 loop-server 保存控制流栈；
- Human 连接可以任意断开。新的 SSE 连接先收到反映当前数据库状态的 AgentUE `start`，然后只接收
  更大 cursor 的变化。

SSE cursor 是存储位置，AgentUE `seq` 是 UI model 的变更顺序；当前实现用持久 cursor 作为已持久
patch 的 `seq`，但仍保持这两个概念在协议上的独立性。

## 一致性规则

1. Message sequence 通过事务内原子递增 `conversations.last_sequence` 分配。
2. 相同 `owner_uid + effect_key` 和相同请求返回已有 Harness Call 或 Interaction；请求内容变化返回
   conflict。
3. Operator 最终 Reply 在一个事务中分配 Message sequence、写 Message、完成 Invocation 并追加事件。
4. provider 请求失败只表示本次观察失败；只有 provider 报告终态才结束 Call。若 provider 可能已执行
   但无法证明 identity，Call 进入 `unknown`，避免重复副作用。
5. 流式事件和终态正交：事件更新 `last_activity_at/cursor`，Call phase 单独表达是否完成。
6. Harness token delta 只唤醒 UI/Call observer；Operator wake stream 仅发布新 Invocation、Human 回答和
   Harness Call 语义状态变化，避免把流式输出放大成 Reconcile 风暴。

## v1 部署边界

SQLite 配置单连接与显式连接生命周期，适合单实例开发和首个垂直切片。多实例部署前应换用共享数据库，
并为 Call 扫描增加租约/claim；provider 幂等 identity 仍是最后一道重复执行防线。
