# loopd Kernel

loopd 是 “Loop is a CRD” 在编排层的实现，也是 Human、Harness 与 Operator 的协作平台。
Human 在持久 Conversation 中提出目标，server 创建 Task 唤醒选中的执行目标，Harness 或
Operator 完成工作并将过程与结果带回 Conversation。

## 定位与组件

loopd 位于 Agent 技术体系的 Orchestrator 层。模型推理、工具循环和 Sandbox 由外部 Harness
及基础设施提供；loopd 协调参与者、持久聊天、Task 分发和可见结果。

- loop-server 提供 Chat API 和页面协作状态，拥有 Conversation、Message 与 Task 分发。
- loop-runtime 是 Operator 的协作开发库，复用 controller-runtime 的资源控制循环，提供公共协作能力。
- Operator 实现具体编排策略，可按复杂度拥有自己的领域 Resource。
- Harness 拥有智能执行状态，通过 Adapter 接入，可被 Operator 调用或直接回答 Human。

Operator 依赖 runtime 公共契约，不依赖 server 私有实现；server 不导入 Operator 领域 CRD，
也不执行 Harness Adapter。新增业务编排或 Harness provider 不应要求修改聊天核心模型。

## 参与者与事实归属

公共消息角色只有 `user`、`operator`、`harness`。模型协议中的 assistant/system/tool 等术语
止于 Adapter，不成为新的公共角色。ActorRef 的 kind/key 表达身份，target 表达本次问答目标。

| 对象 | 语义与 owner |
|---|---|
| Conversation | server 拥有的持久对话空间，历史跨 Operator/Harness 共享 |
| Message | server 保存的一条页面可见表达，content 是 AgentUE 语义快照 |
| Task | server 创建的通用 CRD，提供一次问答的持久路由与唤醒入口 |
| Operator Resource | Operator 拥有的领域目标、状态与完成条件 |
| Harness Execution | Harness 拥有的智能执行及恢复状态 |
| Registration | server 保存的 Operator/Harness 在线发现租约 |

Message 记录 Actor 发出的消息，记录顺序不定义 Actor 的执行顺序。同一 Task 可以包含并行工作；
消息回复只表达回答关系，执行依赖、等待与调度由 Operator 和 Harness 决定。

Activity、Artifact 和流式事件是过程记录或投影，不独立拥有业务执行状态。在线发现也不代替
任务生命周期；注册与 Actor 发现是 runtime 的公共能力，记录由 server 保存。

Conversation 是一个对话框，每次提问可以更换目标。详情 Conversation 挂在某条主回答下，
供页面展开内部工作；最终回答仍由该次问答的目标负责。可见历史属于 server，完整 prompt、
模型事件、tool call/result、重试与成本由 AgentLedger 承载。

## Loop is a CRD

需要跨时间持续收敛的领域 Loop 由 Resource 的 spec/status 与 Reconcile 表达：

```text
Loop = Resource(spec + status) + Reconcile
```

spec 表达目标和约束，status 表达最新观测；Reconciler 读取权威事实，判断继续、等待、重试
还是完成。Event 只提示变化，不能取代状态读取。

通用 Task 携带问答身份、目标路由与唤醒信息，不复制 Query、History、回答或 Operator 领域
状态。简单 Operator 直接 Reconcile Task；复杂 Operator 再创建自己的领域 CRD。LongHorizon
的 Manager、Executor、Auditor 等角色可以由其 Operator 定义，不进入通用内核。

Task 只标记活跃问答。回答固化后结束流并删除 Task；领域长期状态留在 Operator Resource，
完整执行状态留在 Harness。HTTP/SSE 连接只负责观察，断开连接不取消执行。

## 两条协作路径

Human 可以直接请求 Harness，也可以请求 Operator。两条路径都从主 Conversation 的问题
Message、目标的回答 Message 和同一 task_id 的 Task 开始。

```text
Human → Conversation / Message → Task
                                  ├─ Harness → 过程与回答
                                  └─ Operator Reconcile
                                       ├─ 读取会话与领域事实
                                       ├─ 调用一个或多个 Harness
                                       └─ 汇总过程与回答
                              → server → Human
```

Operator 的内部输出可以贡献处理详情，主回答由 Operator 显式汇总。图描述协作边界；
具体 Harness 要成为可直聊目标，还需实现注册、Task 消费和结果发布。

一次问答可以持续几十分钟乃至数天。同一动作重试必须保持稳定身份；收到事件只表示有进展，
不表示成功完成。调用、等待与恢复契约由 [Runtime](runtime.md) 定义。

## 状态与交付边界

数据库保存聊天事实，Kubernetes 保存 Task，Redis 承载活跃问答的页面事件。它们不共享一个
数据库事务，server 负责创建失败的补偿与完成顺序。TaskContext 从主会话即时组装，server
不建立平行的 tasks 表，也不保存 Operator 领域表。

AgentUE 拥有页面语义模型、Reducer、Redis Bridge 和续接。runtime 向任意 server 实例发布
事件，页面按 task_id 从任意实例观察；server 在完成阶段固化 Message。事件传输标识与语义
顺序分别负责续接和幂等，不能将事件流当作完整执行历史。

流式观察跨实例并不自动保证跨重启恢复。长期执行依赖 Harness 的持久状态与相应存储配置；
各能力的恢复边界分别见 Runtime 和 Task 交付文档。

## 领域设计入口

- [Runtime](runtime.md)：相对 Operator 的定位、注册发现、上下文、Harness Call 与结果发布。
- [聊天持久化](../server/docs/persistence.md)：Conversation、Message、详情会话与游标。
- [Task 交付](../server/docs/task-delivery.md)：创建补偿、流式续接、完成与重试。

领域流程和局部约束由以上文档维护；本文件只定义共享模型、主流程与 owner 边界。
