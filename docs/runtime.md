# Loop Runtime

loop-runtime 是嵌入 Operator 的 Go 协作 SDK/toolkit，将 Server HTTP API 与 SSE 封装为数据
读取、发言、流式交付、Human 交互和 Harness 调用的 Verb。controller-runtime 提供 Manager、
Watch、Client 与 Reconcile 调度，runtime 提供接入辅助；Operator 决定业务含义、执行策略与完成条件。

“Loop is a CRD” 不只是资源 CRUD：Reconcile 通过 Verb 把判断连接到真实协作。编排恢复依赖
Operator 持久化的领域 CRD；Harness 恢复由 Adapter 及执行端保证。runtime 不恢复 Go 调用栈，
也不建立通用持久 Effect 引擎。跨组件边界见 [Kernel](kernel.md)。

本文面向 Operator 开发者，定义 Verb 的调用与组合契约；Actor 的整体协作模型见 Kernel，
服务端消费、存储和页面交付机制分别下沉到 server 的领域文档。

## Toolkit 分工

Operator 通过根包组装 Runtime，使用 `Loop.Conv`、`Loop.Human`、`Loop.Harness` 和
`Loop.Operator`。内部依赖沿 `verb → service → infra` 展开，各层共用 `model`：

- verb 表达调用意图，如 Speak 一次说完、Tell 开启流式输出；不处理 HTTP 路径和 SSE 协议。
- service 组合 Server API，实现惰性消息读取、流式写入、Call/Human 等待和注册续租。
  哪些操作可重试、何时检查持久终态属于这一层，不交给通用 HTTP 客户端决定。
- infra 提供有界 HTTP 请求、SSE 解码及传输错误转换，不判断业务是否完成。
- model 保存共用请求、Message/Stream 接口及 Error；已有公共协作数据复用 contract，
  不复制一份同义模型，也不执行 I/O。

这些包直接位于 runtime 下，不另设 internal 层。根包保留组装、controller-runtime 接入辅助和
公共类型入口；下层不反向导入根包，因此错误的共享定义放在 model，`runtime/errors.go` 保留
类型别名与判断入口。消息句柄的实现由 service 拥有，Operator 不必了解传输或存储细节。

## 参与者与接入

Human 与 Operator 在持久会话中独立发言，不要求轮流说话或每条输入对应一条答案。
Operator 可以持续工作，在自己选定的执行边界接收追加消息，并多次回应。

Operator 入口组装 controller-runtime Manager 与 runtime，注册在线身份，
用原生 Builder 监听 Conv CRD，再在 Reconcile 中调用 Verb。runtime 不启动第二套 Manager：

```go
ctrl.NewControllerManagedBy(mgr).
    For(&conversationv1.Conversation{},
        builder.WithPredicates(loopruntime.ConversationPredicate(actor))).
    Complete(reconciler)
```

Watch 是 Controller 配置，不是 Verb。ConversationPredicate 过滤其他 Actor 的信号和单纯的
拉取位置变化；Conv CRD 承载唤醒与消费协调，消息正文通过 Server API 获取。消费契约见
[Conversation](../server/docs/conversation.md)。Participant helper 和句柄 ID/本地快照读取属于
本地操作，不发起网络请求；跨进程协作由 Server API 提供。

Operator 不导入 server 私有 model/repo，不直接写聊天数据库或 Redis。业务自有 API 与领域
CRD 可通过普通 Client 访问，不必进入 loopd Core。Harness Engine 位于 Server，
其中的 HarnessRunner 驱动 Adapter；runtime 通过 API 使用这组能力。

ActorKind 的内置常量为 ActorKindUser、ActorKindOperator、ActorKindHarness。Operator 可声明
`operator/<operator-key>/<role>` 自定义 kind（最长 128 字节），如 LongHorizon Manager 以 Run UID
作为 key。自定义角色能 Speak、Poll、Commit 和发起 Human 交互，拥有独立定向信号和消费位置；
身份不会自动注册为发送框里的服务。Actor 命名不是鉴权，API 仍采用可信部署边界。

Operator 发起 Harness 时，使用 `OperatorHarnessKindFormat` 格式化
`operator/<operator-name>/harness`，具体 Harness 标识放在 Actor.Key。
同一个 Harness 跨消息保留身份；临时 Harness 按业务步骤分配稳定标识，重试不换身份。

## Verb 与 Effect

Verb 表达“可以做什么”，Effect 分为 read 与 write。write 不自动意味着幂等或持久恢复；
每项 Verb 有自己的身份和重试边界。

| 分组 | Verb | Effect 与语义 |
|---|---|---|
| Conv | Read / List | read：创建单条惰性引用／分页发现消息元信息与句柄 |
| Conv | Poll | write：拉取收件消息，记录 Position，不自动提交 |
| Conv | Commit | write：确认连续安全消费前缀 |
| Conv | Speak / Tell | write：一次说完返回 Message／开启流式消息返回 Stream |
| Human | Ask / Confirm | write：创建或复用独立问题 Message |
| Human / Human handle | Get / Wait | read：观察问题的权威结果 |
| Harness | Prompt | write：发起或复用有身份的执行，返回 Call |
| Harness Call | Get / Stream / Wait / Result | read：观察已有执行、等待终态或提取结果 |
| Harness Call | Cancel | write：请求停止 Server 驱动，远端取消取决于 Adapter 能力 |
| Stream handle | Emit / End | write：增量更新／结束这条消息，不管理页面连接 |
| Message handle | Info / Snapshot / Block / Blocks | read：显式获取元信息、完整内容、单块或逐页逻辑块 |
| Message handle | ID / ConversationID | 本地读取身份，不发起网络请求 |
| Operator / Harness | Register | write：注册与续租在线身份 |

read 表达观察意图；server 在读取 Human 状态时推进已到期问题，不等于调用者发起新工作。
Effect 分类不增加额外的 Verbs 容器或独立 CRD。

## Verb 的错误返回与保存

Verb 在正常业务数据之外返回 error（没有业务返回值时只返回 error）；流式观察通过错误通道
交付异步错误。公共错误入口为 `runtime/errors.go`，通过 `errors.As` 读取 `runtime.Error`，
通过 `errors.Is` 保留底层原因的判断。`Retryable` 是可重试提示，不是业务必须重试的指令。
Human 的拒绝、忽略或超时等类型化正常结果，仍由业务返回值表达。

对这些 error 的处理也是 Operator 业务的一部分。runtime Verb 支持错误返回和保存，Operator
决定何时调用、发布什么错误信息，以及之后是否重试、兜底或 Commit。返回 error 本身不会自动
创建错误消息、结束 Harness 或提交输入；保存错误的 Speak/Emit/End 也可能失败，其 error 同样
交由 Operator 处理。

当前 Router 采用简单策略：业务收到错误后发布错误消息，保存成功再 Commit 连续消费前缀。
已知完整错误时，一次 Speak 原子保存 AgentUE `meta.error` 和 `status=failed`，无需开启流式消息：

```go
_, publishErr := loop.Conv.Speak(ctx, convID, contract.SpeakRequest{
    Key: inputID + "/failure", Actor: self, Target: user, ReplyToID: inputID,
    Status: contract.MessageStatusFailed,
    Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{"error":{"code":"operator_failed","message":"处理失败，请重试。"}},"blocks":[]}`),
})
if publishErr != nil {
    return publishErr // 保存失败，尚不能按已报告错误确认消费
}
```

已有流式消息通过 Emit 写入 AgentUE `meta.error`，再调用 End(failed)。End 是 Message 句柄上的
write Verb，只结束这一条消息；Operator 仍需处理 Emit/End 返回的 error。错误内容使用业务选择
的公开说明，不自动序列化底层 Cause 或请求信息。

## 一个 Reconcile 能做什么

下面是能力速览伪代码，不是可直接运行的 Go：省略 context、请求结构、稳定动作身份、错误处理
与恢复分支。每行展示一种协作能力，真实 Operator 按需选择，不必把所有 Verb 串成固定流程。

```go
func Reconcile(conv) {
    convID := conv.Name
    participant, ok := loopruntime.Participant(conv, self) // 从已读取的 Conv CRD 合并 spec/status
    if !ok || participant.ConversationID == "" { return }  // 尚未就绪，不启动工作
    workspace := participant.ConversationID                // server 随定向消息分配的过程会话
    inbox := Loop.Conv.Poll(convID)                         // 收到发给自己的消息，不代表处理完成
    history := Loop.Conv.List(convID, query)                // 只发现元信息与句柄，不加载所有正文
    message := Loop.Conv.Read(convID, messageID)            // 不访问网络；需要时再 message.Snapshot(ctx)
    question := Loop.Human.Ask(convID, "希望怎样处理？")     // 反问用户；handle.Get / Wait 获取选择
    approval := Loop.Human.Confirm(convID, "确认执行吗？")   // 请求确认；普通追加发言不等于同意
    call := Loop.Harness.Prompt(workspace, prompt, tools)   // 按选择与确认结果调用 Harness，立即取得句柄
    progress, err := call.Get(ctx); events, errors := call.Stream(ctx)      // 查看执行状态或持续观察增量
    result := call.Wait()                                  // 需要结果时再等；也可以先返回，之后再调谐
    Loop.Conv.Speak(convID, result)                        // 已知完整内容，一次说完，无需 End
    stream := Loop.Conv.Tell(convID)                       // 需要逐步输出时，取得流式句柄
    stream.Emit(event); stream.End()                       // 增量输出，最后结束这条消息；页面继续订阅
    Loop.Conv.Commit(convID, inbox.Position)               // 仅提交已经安全处理的连续消息前缀
}
```

Ask/Confirm 得到有效结果后才进入依赖它们的步骤；取消、超时和拒绝由 Operator 决定如何收口。
Harness 的可见输出由 Server 自动持久化与交付，`call.Stream` 用于 Operator 自己观察，不必再转发一遍；
`stream.Emit` 展示的是 Operator 自己逐步发言的能力，不是再转发一次 Harness 输出。追加消息何时再次 Poll，也由业务决定。
角色调用通过 Prompt.Actor 与 Meta 指定输出身份和展示信息，由 Server 创建独立 Message。

`Loop.Operator.Register(...)` 与可选的 `Loop.Harness.Register(...)` 在启动时登记在线身份并续租，
不在每次 Reconcile 里调用。Watch 属于 controller-runtime 的启动配置，不是 Verb。

## 会话、消息与发言

Poll 返回本次接收的消息，Conv.List 分页发现会话消息，不递归合并内部会话，也不改变消费位置。
Operator 自行选择历史范围并组装执行上下文，runtime 不定义独立的上下文模型或问答配对。

Message 是只读接口，不是预先加载的正文。Conv.Read(convID, messageID) 只建立引用；
ID/ConversationID 不访问网络，Info、Snapshot、Block、Blocks 在显式调用时读取 Server。
List 支持 IDs、开区间边界 Before/After、正反序、状态与数量筛选，返回本次元信息和 Message 句柄；
Before/After 的边界均不包含自身，可同时限定一个区间。Info 每次读取当前元信息，不复用 List 的旧值。
读取始终限定在指定 Conversation 内，单条查询不靠扫描历史寻找 ID。

Snapshot 返回同一版本的完整内容；Block 按逻辑 block ID 获取内容，Blocks 逐页遍历。
分页期间消息版本改变会返回冲突，调用者丢弃部分结果后重读，不能混合不同版本。
正文读取不按逻辑大小拒绝已成功写入的内容；长消息可按需读取逻辑块，请求仍受超时约束。
这些选择与底层如何存储无关：Part 只是 Server repo/model 的存储优化，不是外部协作概念，
runtime、Operator 与 Harness 都不解析引用或感知 Part。细节见 [持久化](../server/docs/persistence.md#message-内容与-parts)。

Conv.Speak 创建或复用一条 Actor-owned Message，稳定 Key 的范围是 Conv + Actor。
同 key 重试返回既有消息，不覆盖已有正文；改变收件者或回复引用会冲突。需要新发言用新 key，
流式更新通过返回的消息句柄发布。内容是 AgentUE model，不限于文字。

Speak 一次说完：Content 随消息原子保存并标记结束，返回只读 Message，不需要 End。
Tell 保持消息开放，可携带初始 Content，也可省略，返回 Stream；之后用 `stream.Emit` 发布
AgentUE set/append，最后 `stream.End`。Stream 嵌入 Message 接口，共享相同的惰性读取能力，
但读句柄不能获得写入权限，runtime 不保留随输出增长的正文副本。
Speak 可指定终态 Status，默认 completed；失败说明用 failed，并在 Content 中携带
AgentUE meta.error。Tell 创建 streaming 消息，终态通过 End 指定。正文和 Status 只在首次创建时
生效，同 Key 重试返回既有消息，不能覆写内容、改变终态或重新打开消息。

`stream.End(ctx)` 默认将 Message.status 从 streaming 改为 completed；输出异常或明确取消时，
可传 `stream.End(ctx, contract.MessageStatusFailed)` 或 `contract.MessageStatusCancelled`。
相同终态的 End 可重复调用，不同终态冲突，结束后不再接受 Emit；重新 Tell 同 Key 可恢复写入句柄的状态与 Revision。
句柄只属于一条消息，不关闭 Conv、不 Commit，也不结束其他 Actor 的工作或页面订阅。

Speak 不依赖某次 user input 或页面连接。Target 可以是 User、其他 Operator，或留空向会话发言；
reply_to_id 表达回应哪条消息，Target 表达说给谁听，两者不能互相替代。
页面实时观察流式内容；Poll、Read 和 List 都允许读取任意状态的消息，包括 streaming。
是否利用部分内容、何时等待、何时 Commit 由 Operator 决定。Poll 按消息 ID 而非 revision 推进；
越过某条消息后，要跟进其后续内容应保留 ID 再 Read，或通过 Harness Call 观察。
failed/cancelled 可保留已输出的部分内容，不能当作完整成功结果。

server 在 User conv 接收定向消息时，按父会话 + 完整 Actor 身份创建或复用过程会话，
在通知中写入 `spec.participants[].conversationID`。Operator 从 Conv CRD 取得 ID，将内部协作
消息写入过程会话，面向用户的消息仍写入主会话；Poll/Commit 始终针对接收输入的主会话。
过程会话只组织可见消息，不定义业务执行或恢复边界。

`loopruntime.Participant(conv, actor)` 是本地读取 helper，不是网络 Verb。它按 kind/key 合并
参与者的 ConversationID、EndOffset 与消费状态 Position、Committed；成员不存在时返回 false。
Operator 在启动新工作前检查 ConversationID，关联尚未投影时等待重试，不提交待处理输入。
分配、原子写入与通知重试见 [持久化](../server/docs/persistence.md) 和
[Conversation](../server/docs/conversation.md)。

## 消费与连续输入

Operator 把 DB 中持久化的消息当作自己的输入 queue，经 runtime 消费，不直接连接数据库。
Poll/Commit 在 runtime 内均调用 Server HTTP API，由 Server 协调 DB 消息读取和 Conv CRD
消费位置更新；Operator 不自行改写这些游标。
各 Operator 独立选择 Poll、Speak 和 Commit 的时机；持续输入是常态，不需要等当前回答结束
才能提交下一条消息。Commit 应跟随可安全恢复的处理进度，而不是仅仅跟随 Poll 返回。

Poll / Commit 参考 Kafka 的消费语义。Poll 不传 After 时从 Committed 恢复；同一次执行继续
拉取时传上次返回的 Position，响应丢失则用相同参数重试。只有安全处理了连续前缀，才 Commit。
位置定义、通知重试与至少一次消费的限制见 [Conversation](../server/docs/conversation.md)。

何时接受补充、重做计划还是继续执行，属于 Operator 业务。runtime 不把普通消息自动映射成
Harness steer/followup，也不规定一条消息就是一个新任务。Read 不改变消费位置。

## Harness：提交与观察

Harness 调用由 Server 内部 Harness Engine 管理与驱动。loop-runtime 是 Operator toolkit，提交调用并
返回可重建的远程句柄；不注入 Adapter、不持有完整事件数组，也不接管 Agent 内部执行状态。

```go
call, err := loop.Harness.Prompt(ctx, loopruntime.Prompt{
    ConversationID: workspaceID,
    IdempotencyKey: businessKey,
    EffectKey: "plan",
    Target: "managed",
    Text: prompt,
    Timeout: 30 * time.Minute,
})
if err != nil { return err }
result, err := call.Result(ctx) // result.Format + result.Content；Text() 提供文本视图
```

Prompt 遇到 Server 容量不足时返回 nil Call 与统一 runtime Error，不创建新调用。用
`IsHarnessCapacityExceeded(err)` 判断；`IsRetryable(err)` 为 true，表示 Operator 可以稍后重试，
SDK 不自动重试容量拒绝。相同幂等 key 的既有调用不受新调用容量限制，详见
[容量与拒绝](harness.md#容量与拒绝)。

Call.Get(ctx) 通过 API 读取状态，Stream(ctx) 通过 Server 的 Run SSE 接口观察 AgentUE 增量，
实时事件来源是 Redis。Wait(ctx) 在流结束或中断时查询持久状态，可重试的中断重新连接同一
Call，最多三次连接；Result(ctx) 从持久终态提取结果。正常流不轮询调用状态。
`loop.Harness.Call(runID)` 重建句柄，不重新提交。取消等待、关闭 toolkit 均不取消执行；显式
Cancel(ctx) 请求停止 Server 驱动，远端是否中断由 Adapter 能力决定。

Call.Message(ctx) 返回同一 Message 只读接口，不负责开始或结束执行。Prompt 返回时已经有消息引用；
通过 Run ID 重建的 Call 则先读取 Run 元信息解析引用，不加载正文。Get 在成功态按需读取
result block，不为提取最终结果展开整条执行消息。Call 生命周期与消息读写能力保持分离。

Server 在接收事务中创建输出 Message，调用者可以指定开放的 Actor kind/key、Recipient 和展示
Meta。一个 Call 对应一条独立输出，不再接受调用者的 Output writer；Server 原子保存 result block
和执行终态。Operator 读取 text/JSON 作决策，不接手 Emit/End；自己的总结等发言仍使用 Speak。

调用 API、幂等、接管、租约、结果格式及 Adapter 配置统一见
[Harness Engine](harness.md)。Wait 会占用 Reconcile 并发位；不等待时用
Get + RequeueAfter，完成不会自动映射成业务 CRD Watch。

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

Operator 只调用 Speak、Tell、Emit、End，不接触 Delivery、task_id、Redis Event ID 或 SSE 连接。
发送成功表示 DB 已接收可见消息；runtime/server 负责增量持久化、通知和间接送达页面。
Redis 暂时不可用不要求 Operator 重做业务；页面通过持久快照追上，详见 [UE](../server/docs/ue.md)。

stream.Emit 接收 AgentUE set/append，忽略调用方的 Seq，由句柄串行分配序号并有界重试瞬时失败。
同 Key 的 Tell 复用消息并从 DB 重建句柄；重启后依据持久 Revision 恢复写入位置，不恢复 Go 调用栈。
一条消息须由一个逻辑写入者拥有，多副本执行互斥仍由 Operator 配置。

重试耗尽仍返回错误，未确认的更新不能被下一条内容越过；调用方可重试同一更新，或结束本轮执行
交由自身恢复策略处理。业务检查点决定哪些内容仍需输出，runtime 不推断哪些 token 已被处理。
完整执行事件、工具输入输出与成本属于 AgentLedger，不由可见 Message 代替审计。

## 注册与发现

Operator.Register、Harness.Register 按 kind/key 注册并随 runtime 生命周期续租。
server 的 actors 接口只列未过期 Operator/Harness；Human 不需要注册。注册记录不是领域配置，
租约也不是执行锁。多副本互斥与分片由 Operator 配置，不由心跳保证。

Harness 的配置 target、在线注册和输出 Actor 身份各有用途，统一见
[Harness 管理](harness.md#管理配置注册与身份)。Router 只注册自身，按需调用 Server 配置的 Harness。

## Router 示例策略

Router 直接 Reconcile Conv，不创建 Work CRD。Poll 到输入后，用 List 选取该输入之前的有限历史，
执行 plan → 有界并行 Harness。当前批结束后再 Poll：有追加输入时先发阶段结果，再带累计证据
重新 plan，决定继续分派或汇总。没有新输入则发出该输入快照的汇总结果。

汇总期间到达的消息留给下一次 Reconcile，持续输入不能让当前结果无限推迟。
完整发言成功或明确发出失败说明后，再 Commit 连续消费前缀；不等待页面关闭。
这是 Router 的策略，不是 runtime 强制的交互回合。

示例循环状态在内存，未提交输入可以重读，但计划与结果不会凭空恢复；需要持久工作时由 Operator
保存领域状态，并接入可恢复 Adapter。不接入 steer/followup 不影响未来按业务策略扩展。

## 实现与验证入口

[Conv](../runtime/verb/conversation.go)、[消息句柄](../runtime/model/message.go)、
[Human](../runtime/verb/human.go)、[Harness](../runtime/verb/harness.go) 和
[Router](../operators/router/internal/router/router.go) 是能力入口。
Go 测试覆盖消费重读、交付和交互，Web 测试覆盖消息投影与卡片展示。
页面交互与交付协议见 [UE](../server/docs/ue.md)。

## 领域 Operator 示例

[LongHorizon](../operators/longhorizon/README.md) 展示 Conv intake、三个业务 Reconciler、轮次边界
接收补充输入、报告检查点和 Operator 自己的 Run TTL。具体策略由其 [领域设计](../operators/longhorizon/docs/design.md)
拥有，不成为 runtime 对所有 Operator 的约束。
