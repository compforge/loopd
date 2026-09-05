# Loop Runtime

loop-runtime 是嵌入 Operator 的 Go 协作 toolkit。controller-runtime 提供 Manager、Watch、
Client 与 Reconcile 调度，loop-runtime 联合 server 提供数据读取、发言、流式交付、Human 交互
和 Harness 调用的 Verb。Operator 决定业务含义、执行策略与完成条件。

“Loop is a CRD” 不只是资源 CRUD：Reconcile 通过 Verb 把判断连接到真实协作。编排恢复依赖
Operator 持久化的领域 CRD；Harness 恢复由 Adapter 及执行端保证。runtime 不恢复 Go 调用栈，
也不建立通用持久 Effect 引擎。跨组件边界见 [Kernel](kernel.md)。

本文面向 Operator 开发者，定义 Verb 的调用与组合契约；独立参与者的整体协作模型见 Kernel，
服务端消费、存储和页面交付机制分别下沉到 server 的领域文档。

## 参与者与接入

Human 与 Operator 在持久会话中独立发言，不要求轮流说话或每条输入对应一条答案。
Operator 可以持续工作，在自己选定的执行边界接收追加消息，并多次回应。

Operator 入口组装 controller-runtime Manager 与 runtime，注入 Adapter，注册在线身份，
用原生 Builder 监听 Conv CRD，再在 Reconcile 中调用 Verb。runtime 不启动第二套 Manager：

```go
ctrl.NewControllerManagedBy(mgr).
    For(&conversationv1.Conversation{},
        builder.WithPredicates(loopruntime.ConversationPredicate(actor))).
    Complete(reconciler)
```

Watch 是 Controller 配置，不是 Verb。ConversationPredicate 过滤其他 Actor 的信号和单纯的
拉取位置变化；消费契约见 [Conversation](../server/docs/conversation.md)。

Operator 不导入 server 私有 model/repo，不直接写聊天数据库或 Redis。业务自有 API 与领域
CRD 可通过普通 Client 访问，不必进入 loopd Core。Harness Adapter 装配在 Operator 进程，
server 不执行 Adapter。

## Verb 与 Effect

Verb 表达“可以做什么”，Effect 分为 read 与 write。write 不自动意味着幂等或持久恢复；
每项 Verb 有自己的身份和重试边界。

| 分组 | Verb | Effect 与语义 |
|---|---|---|
| Conv | Read / Context | read：共享历史／截至某条 Message 的有界上下文 |
| Conv | Poll | write：拉取收件消息，记录 Position，不自动提交 |
| Conv | Commit | write：确认连续安全消费前缀 |
| Conv | Speak | write：Actor 在指定 Conv 中发一条消息 |
| Conv | Workspace | write：懒创建或复用该 Actor 的内部会话 |
| Human | Ask / Confirm | write：创建或复用独立问题 Message |
| Human handle | Get / Wait | read：观察问题的权威结果 |
| Harness | Prompt | write：发起或复用有身份的执行，返回 Call |
| Harness Call | Value / Stream / Wait | read：观察已有执行 |
| Delivery | EmitMessage / Complete | write：按 Message 更新可见流／关闭页面交付 |
| Operator / Harness | Register | write：注册与续租在线身份 |

read 表达观察意图；server 在读取 Human 状态时推进已到期问题，不等于调用者发起新工作。
Effect 分类不增加额外的 Verbs 容器或独立 CRD。

## 会话、消息与发言

Conv.Context 按 conversationID + messageID 返回 MessageContext，内容是会话、当前消息与截至该
消息的有界 History，不预设 input/response 配对。Conv.Read 分页读整个会话，不递归合并内部会话。

Conv.Speak 创建或复用一条 Actor-owned Message，稳定 Key 的范围是 Conv + Actor。
同 key 重试返回既有消息，不覆盖已有正文；改变收件者或回复引用会冲突。需要新发言用新 key，
更新已有消息则按其 Message ID 发布事件。内容是 AgentUE model，不限于文字。

Speak 的 TaskID 可选，只用于页面交付关联。它既不依赖一个尚未完成的 user input，也不结束
Operator 的工作。reply_to_id 表达回应哪条消息，target 表达说给谁听，两者不能互相替代。

Conv.Workspace 按 User conv + Actor 懒创建并复用内部会话。Operator 决定哪些信息面向用户，
哪些属于内部协作；Toolkit 承担工作会话的分配细节。归属见
[持久化](../server/docs/persistence.md)。

## 消费与连续输入

Operator 把 DB 中持久化的消息当作自己的输入 queue，经 runtime 消费，不直接连接数据库。
各 Operator 独立选择 Poll、Speak 和 Commit 的时机；持续输入是常态，不需要等当前回答结束
才能提交下一条消息。Commit 应跟随可安全恢复的处理进度，而不是仅仅跟随 Poll 返回。

Poll / Commit 参考 Kafka 的消费语义。Poll 不传 After 时从 Committed 恢复；同一次执行继续
拉取时传上次返回的 Position，响应丢失则用相同参数重试。只有安全处理了连续前缀，才 Commit。
位置定义、通知重试与至少一次消费的限制见 [Conversation](../server/docs/conversation.md)。

何时接受补充、重做计划还是继续执行，属于 Operator 业务。runtime 不把普通消息自动映射成
Harness steer/followup，也不规定一条消息就是一个新任务。Read 不改变消费位置。

## Harness 执行与恢复

Harness.Prompt 返回 Call handle。Operator 提供 prompt、tools、目标、输出 ConversationID 和
稳定 IdempotencyKey；EffectKey 是业务步骤名。动作身份不能在每次 Reconcile 中随机变化。

同一 runtime 内同身份同参数复用 Call，变化冲突。生产 Adapter 必须把相同身份映射到相同持久
执行，并在重启后重新观察；agentd 可以承担持久执行，内置 agentgo 只是进程内 demo。
TaskID 仅作为可选的页面交付关联，不替代业务 IdempotencyKey。

Call.Value 读取状态，Stream 观察增量，Wait 等待终态。耗时长不代表没有进展。Wait 的 context
取消只结束等待；runtime 关闭会结束进程内执行，外部持久执行仍由 Harness 拥有。

Wait 会占用 Reconcile 并发位。不等待时应安排 RequeueAfter 或自己的资源 Watch；Conv Watch
不自动把 Harness 完成事件映射成调谐。Call 的本地事件缓冲不等于持久订阅。

## Human：Ask 与 Confirm

Ask/Confirm 以 ConversationID、提问 Actor、目标 User、EffectKey、问题和有限正 Timeout
定义一项交互，可选 ReplyToID 与 TaskID。问题与答复直接存为 Message，不另建 Interaction 表。
相同 Conv/Actor/EffectKey 同参数返回原问题、deadline 和结果；不同参数冲突。

| Verb | 输入与正常返回值 |
|---|---|
| Ask | Choices 的 value/label 必须非空且 value 唯一；AllowOther 允许非空自由文本 |
| Confirm | 返回 accepted 或 declined；展示标签不改变这两个值 |

Ask 可以是封闭选项、选项加自由文本，或不提供 Choices 且 AllowOther=true 的纯自由文本。
拒绝 Confirm 是 success(declined)，忽略是 dismissed，不等于批准。

handle.Get/Wait 返回 pending 或不可变终态：success(value)、dismissed、timeout、failure(reason)。
Timeout 必填，server 首次持久化时确定 deadline，重试不重置。dismissed/timeout 是正常业务结果，
不自动取消其他问题或 Harness。只有 success(accepted) 是明确同意。

普通发言由 Operator 自行理解；类型化答复必须携带精确 reply_to_id，不能用最近消息、Actor、
时间或 TaskID 猜测配对。同一 Conv 可以有多个问题，乱序答复也各自收口。
server 原子校验身份、期限与结果，创建 user 回复并通知提问者；重复相同答复幂等，矛盾或迟到答复拒绝。

问题使用受控 ask/confirm block，回复使用 human_reply block。普通 Speak 与流式写入不能伪造
这些块或修改其受控 meta。超时不伪造 user Message，也不把正常 dismissed/timeout 渲染成异常。

UI delivery 关闭、浏览器断线或 Wait context 取消均不终止问题。问题仍可答复，直到其自身 deadline
或有效终态。Human 维护到期与通知，通用交付收尾重试由 Delivery 生命周期负责。

Quick Start 用 HttpOnly Cookie 的摘要标识 User；托管方可通过 HumanIdentity 接入登录身份。
只有问题指定的目标 User 可以作答；任意 user_key 字段不能冒充身份。
Operator API 与历史读取仍是可信部署边界，不提供完整多租户 ACL。

## Delivery：页面传输而非业务回合

Delivery 封装提交／replay、按 Message 发事件、以及 UI 交付关闭。task_id 只关联 Redis 与页面流。
消息上下文统一通过 Conv.Context/Read 获取，不提供按 task_id 推断问答上下文的入口。

发言使用 Conv.Speak，再用 Delivery.EmitMessage 指定 Message ID，不需要传入 task_id。
每条消息独立更新，不受某条 UI 流的开放状态限制。
消息 end 与 UI delivery end 不同；结束页面流不禁止后续 Speak、Ask 或 Confirm。

同一 runtime 按 Message 串行分配 AgentUE seq，不是多个进程的全局序号服务。
恢复写入方应从持久 Message.Revision 与 Adapter 检查点恢复顺序；Redis Event ID 只是传输续接位置。
完整模型事件、工具执行与成本属于 AgentLedger，不由可见 Message 代替审计。

## 注册与发现

Operator.Register、Harness.Register 按 kind/key 注册并随 runtime 生命周期续租。
server 的 actors 接口只列未过期 Operator/Harness；Human 不需要注册。注册记录不是领域配置，
租约也不是执行锁。多副本互斥与分片由 Operator 配置，不由心跳保证。

内部临时 Harness 无需注册为用户可选目标。Router 只注册自身，按需调用配置的临时 Harness；
注册 Harness 的选择、转发和分派策略由 Router 后续扩展。

## Router 示例策略

Router 直接 Reconcile Conv，不创建 Work CRD。Poll 到输入后，用 MessageContext 读取历史，
执行 plan → 有界并行 Harness。当前批结束后再 Poll：有追加输入时先发阶段结果，再带累计证据
重新 plan，决定继续分派或汇总。没有新输入则发出该输入快照的汇总结果。

汇总期间到达的消息留给下一次 Reconcile，持续输入不能让当前结果无限推迟。
发言成功或明确发出失败说明后，关闭关联 UI 流，再 Commit 连续消费前缀；这些 ID 不定义业务任务。
这是 Router 的策略，不是 runtime 强制的交互回合。

示例循环状态在内存，未提交输入可以重读，但计划与结果不会凭空恢复；需要持久工作时由 Operator
保存领域状态，并接入可恢复 Adapter。不接入 steer/followup 不影响未来按业务策略扩展。

## 实现与验证入口

[Conv](../runtime/conversation.go)、[Delivery](../runtime/delivery.go)、
[Human](../runtime/human.go)、[Harness](../runtime/harness.go) 和
[Router](../operators/router/internal/router/router.go) 是能力入口。
Go 测试覆盖消费重读、交付和交互，Web 测试覆盖消息投影与卡片展示。
页面交互与交付协议见 [UE](../server/docs/ue.md)。
