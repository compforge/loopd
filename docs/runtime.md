# Loop Runtime

loop-runtime 是嵌入 Operator 的 Go 协作 toolkit。controller-runtime 提供 Manager、Watch、
Client 与 Reconcile 调度，loop-runtime 联合 server 提供数据读取、发言、流式交付、Human 交互
和 Harness 调用的 Verb。Operator 决定业务含义、执行策略与完成条件。

“Loop is a CRD” 不只是资源 CRUD：Reconcile 通过 Verb 把判断连接到真实协作。编排恢复依赖
Operator 持久化的领域 CRD；Harness 恢复由 Adapter 及执行端保证。runtime 不恢复 Go 调用栈，
也不建立通用持久 Effect 引擎。跨组件边界见 [Kernel](kernel.md)。

本文面向 Operator 开发者，定义 Verb 的调用与组合契约；Actor 的整体协作模型见 Kernel，
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

ActorKind 的内置常量为 ActorKindUser、ActorKindOperator、ActorKindHarness。Operator 可声明
`operator/<operator-key>/<role>` 自定义 kind（最长 128 字节），如 LongHorizon Manager 以 Run UID
作为 key。自定义角色能 Speak、Poll、Commit 和发起 Human 交互，拥有独立定向信号和消费位置；
身份不会自动注册为发送框里的服务。Actor 命名不是鉴权，API 仍采用可信部署边界。

## Verb 与 Effect

Verb 表达“可以做什么”，Effect 分为 read 与 write。write 不自动意味着幂等或持久恢复；
每项 Verb 有自己的身份和重试边界。

| 分组 | Verb | Effect 与语义 |
|---|---|---|
| Conv | Read | read：分页读取共享消息历史 |
| Conv | Poll | write：拉取收件消息，记录 Position，不自动提交 |
| Conv | Commit | write：确认连续安全消费前缀 |
| Conv | Speak | write：一次说完，或开启流式消息并返回句柄 |
| Conv | Workspace | write：懒创建或复用该 Actor 的内部会话 |
| Human | Ask / Confirm | write：创建或复用独立问题 Message |
| Human / Human handle | Get / Wait | read：观察问题的权威结果 |
| Harness | Prompt | write：发起或复用有身份的执行，返回 Call |
| Harness Call | Value / Stream / Wait | read：观察已有执行 |
| Message handle | Emit / End | write：增量更新／结束这条消息，不管理页面连接 |
| Message handle | ID / Value | read：观察消息身份／本地已知快照 |
| Operator / Harness | Register | write：注册与续租在线身份 |

read 表达观察意图；server 在读取 Human 状态时推进已到期问题，不等于调用者发起新工作。
Effect 分类不增加额外的 Verbs 容器或独立 CRD。

## 一个 Reconcile 能做什么

下面是能力速览伪代码，不是可直接运行的 Go：省略 context、请求结构、稳定动作身份、错误处理
与恢复分支。每行展示一种协作能力，真实 Operator 按需选择，不必把所有 Verb 串成固定流程。

```go
func Reconcile(convID) {
    inbox := Loop.Conv.Poll(convID)                         // 收到发给自己的消息，不代表处理完成
    history := Loop.Conv.Read(convID)                       // 主动查看共享历史，不改变消费位置
    workspace := Loop.Conv.Workspace(convID, self)          // 复用内部工作会话，不干扰主会话
    question := Loop.Human.Ask(convID, "希望怎样处理？")     // 反问用户；handle.Get / Wait 获取选择
    approval := Loop.Human.Confirm(convID, "确认执行吗？")   // 请求确认；普通追加发言不等于同意
    call := Loop.Harness.Prompt(workspace, prompt, tools)   // 按选择与确认结果调用 Harness，立即取得句柄
    progress := call.Value(); events := call.Stream()      // 查看执行状态或持续观察增量
    result := call.Wait()                                  // 需要结果时再等；也可以先返回，之后再调谐
    Loop.Conv.Speak(convID, result)                        // 已知完整内容，一次说完，无需 End
    stream := Loop.Conv.Speak(convID, Stream: true)         // 需要逐步输出时，取得消息句柄
    stream.Emit(event); stream.End()                       // 增量输出，最后结束这条消息；页面继续订阅
    Loop.Conv.Commit(convID, inbox.Position)               // 仅提交已经安全处理的连续消息前缀
}
```

Ask/Confirm 得到有效结果后才进入依赖它们的步骤；取消、超时和拒绝由 Operator 决定如何收口。
Harness 的可见输出由 runtime 自动交付，`call.Stream` 用于 Operator 自己观察，不必再转发一遍；
`stream.Emit` 展示的是 Operator 自己逐步发言的能力，不是再转发一次 Harness 输出。追加消息何时再次 Poll，也由业务决定。

`Loop.Operator.Register(...)` 与可选的 `Loop.Harness.Register(...)` 在启动时登记在线身份并续租，
不在每次 Reconcile 里调用。Watch 属于 controller-runtime 的启动配置，不是 Verb。

## 会话、消息与发言

Poll 返回本次接收的消息，Conv.Read 分页读取会话历史，不递归合并内部会话，也不改变消费位置。
Operator 自行选择历史范围并组装执行上下文，runtime 不定义独立的上下文模型或问答配对。

Conv.Speak 创建或复用一条 Actor-owned Message，稳定 Key 的范围是 Conv + Actor。
同 key 重试返回既有消息，不覆盖已有正文；改变收件者或回复引用会冲突。需要新发言用新 key，
流式更新通过返回的消息句柄发布。内容是 AgentUE model，不限于文字。

Speak 默认一次说完：Content 随消息原子保存并标记结束，不需要独立消息 Redis 流，也不需要 End。
只有 `Stream: true` 才保持消息开放；可携带初始 Content，也可省略，之后用 `stream.Emit` 发布
AgentUE set/append，最后 `stream.End`。两种模式返回同一 Go 句柄类型，ID/Value 可读取身份与快照。
模式只在首次创建时生效，同 Key 重试不能把已结束消息重新打开。

End 可重复调用，结束后不再接受 Emit；重新 Speak 同 Key 可取回结束状态与 Revision。
句柄只属于一条消息，不关闭 Conv、不 Commit，也不结束其他 Actor 的工作或页面订阅。

Speak 不依赖某次 user input 或页面连接。Target 可以是 User、其他 Operator，或留空向会话发言；
reply_to_id 表达回应哪条消息，Target 表达说给谁听，两者不能互相替代。
页面实时观察流式内容；其他 Operator 的 Poll 在 End 后收到完整消息，不消费半条流式输出。

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

Prompt.Actor 可指定合法非 user 作者，默认仍为 Harness/Call ID；Prompt.Timeout 交给 Adapter
落实期限。Actor、Timeout 和 ConversationID 等参数参与同一 runtime 的幂等冲突检查。

Call.Value 读取状态，Stream(ctx) 观察本地 AgentUE 增量（不需要页面游标），Wait 等待终态。耗时长不代表没有进展。Wait 的 context
取消只结束等待；runtime 关闭会结束进程内执行，外部持久执行仍由 Harness 拥有。

Wait 会占用 Reconcile 并发位。不等待时应安排 RequeueAfter 或自己的资源 Watch；Conv Watch
不自动把 Harness 完成事件映射成调谐。Call 的本地事件缓冲不等于持久订阅。

## Human：Ask 与 Confirm

Ask/Confirm 以 ConversationID、提问 Actor、目标 User、EffectKey、问题和有限正 Timeout
定义一项交互，可选 ReplyToID。问题与答复直接存为 Message，不另建 Interaction 表。
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
或有效终态。Human 维护到期与通知，消息输出结束与 Human 结果是不同的边界。

Quick Start 用 HttpOnly Cookie 的摘要标识 User；托管方可通过 HumanIdentity 接入登录身份。
只有问题指定的目标 User 可以作答；任意 user_key 字段不能冒充身份。
Operator API 与历史读取仍是可信部署边界，不提供完整多租户 ACL。

## 消息发送与技术边界

Operator 只调用 Speak、Emit、End，不接触 Delivery、task_id、Redis Event ID 或 SSE 连接。
发送成功表示 DB 已接收可见消息；runtime/server 负责增量持久化、通知和间接送达页面。
Redis 暂时不可用不要求 Operator 重做业务；页面通过持久快照追上，详见 [UE](../server/docs/ue.md)。

stream.Emit 接收 AgentUE set/append，忽略调用方的 Seq，由句柄串行分配序号并有界重试瞬时失败。
同 Key 的 Speak 复用消息和本地句柄；重启后依据持久 Revision 恢复写入位置，不恢复 Go 调用栈。
一条消息须由一个逻辑写入者拥有，多副本执行互斥仍由 Operator 配置。

重试耗尽仍返回错误，未确认的更新不能被下一条内容越过；调用方可重试同一更新，或结束本轮执行
交由自身恢复策略处理。业务检查点决定哪些内容仍需输出，runtime 不推断哪些 token 已被处理。
完整执行事件、工具输入输出与成本属于 AgentLedger，不由可见 Message 代替审计。

## 注册与发现

Operator.Register、Harness.Register 按 kind/key 注册并随 runtime 生命周期续租。
server 的 actors 接口只列未过期 Operator/Harness；Human 不需要注册。注册记录不是领域配置，
租约也不是执行锁。多副本互斥与分片由 Operator 配置，不由心跳保证。

内部临时 Harness 无需注册为用户可选目标。Router 只注册自身，按需调用配置的临时 Harness；
注册 Harness 的选择、转发和分派策略由 Router 后续扩展。

## Router 示例策略

Router 直接 Reconcile Conv，不创建 Work CRD。Poll 到输入后，用 Read 选取该输入之前的历史，
执行 plan → 有界并行 Harness。当前批结束后再 Poll：有追加输入时先发阶段结果，再带累计证据
重新 plan，决定继续分派或汇总。没有新输入则发出该输入快照的汇总结果。

汇总期间到达的消息留给下一次 Reconcile，持续输入不能让当前结果无限推迟。
完整发言成功或明确发出失败说明后，再 Commit 连续消费前缀；不等待页面关闭。
这是 Router 的策略，不是 runtime 强制的交互回合。

示例循环状态在内存，未提交输入可以重读，但计划与结果不会凭空恢复；需要持久工作时由 Operator
保存领域状态，并接入可恢复 Adapter。不接入 steer/followup 不影响未来按业务策略扩展。

## 实现与验证入口

[Conv](../runtime/conversation.go)、[消息句柄](../runtime/message.go)、
[Human](../runtime/human.go)、[Harness](../runtime/harness.go) 和
[Router](../operators/router/internal/router/router.go) 是能力入口。
Go 测试覆盖消费重读、交付和交互，Web 测试覆盖消息投影与卡片展示。
页面交互与交付协议见 [UE](../server/docs/ue.md)。

## 领域 Operator 示例

[LongHorizon](../operators/longhorizon/README.md) 展示 Conv intake、三个业务 Reconciler、轮次边界
接收补充输入、报告检查点和 Operator 自己的 Run TTL。具体策略由其 [领域设计](../operators/longhorizon/docs/design.md)
拥有，不成为 runtime 对所有 Operator 的约束。
