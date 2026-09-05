# Loop Runtime

loop-runtime 是嵌入 Operator 进程的 Go 协作开发库。它在 controller-runtime 的控制循环之上，
提供接入 loopd、读取协作上下文、调用 Harness 和发布结果的公共能力，使业务可以用普通
Reconcile 代码实现自己的编排。本文是这些开发契约的唯一设计入口；跨组件事实归属见
[Kernel](kernel.md)。

## 相对 Operator 的定位

controller-runtime 为 Kubernetes Controller 提供 Manager、Client、Cache、Watch 与队列，
Operator 提供 Resource 和 Reconciler 中的业务逻辑。loop-runtime 沿用这种库与业务的关系：
它沉淀多个 Operator 都需要的协作机制，Operator 决定目标意味着什么、下一步做什么以及何时完成。

两者在同一 Operator 内组合。controller-runtime 负责资源控制循环，loop-runtime 补充 Human、
Conversation 与 Harness 的协作能力；已有的 Manager、调度队列、重试和资源 Client 直接复用。

| 关注点 | controller-runtime | loop-runtime | Operator |
|---|---|---|---|
| 进程与调度 | Manager 启停 Controller，提供共享依赖、队列与并发控制 | 管理协作 Client、注册续租与本地 Call 观察 | 组装两套库，配置运行范围与生命周期 |
| 唤醒与读取 | Watch 资源，将变化映射成 Reconcile 请求；Client 读取当前状态 | 将发给自身的 Task 接入 Controller，解析会话上下文 | 定义领域 Resource、Watch 关系，读取外部业务事实 |
| 推进工作 | 调用 Reconciler，按其返回值重试或再次排队 | 提供 Harness Call、可见过程和回答发布契约 | 决定拆解、串并行、工具、角色与完成条件 |
| 状态与恢复 | 根据资源当前状态重新调谐 | 保持协作引用和动作身份，连接各事实 owner | 保存领域进度，重新读取事实后决定如何继续 |

```text
Operator process
  ├─ 领域 Resource / Reconciler / 外部系统 Client
  ├─ controller-runtime：Manager / Controller / Watch / Client
  │                         └─ Kubernetes API
  └─ loop-runtime
       ├─ Chat / Task context / Registration ── loop-server
       ├─ Task.Watch ── controller-runtime
       └─ Harness.Prompt ── Adapter ── Harness
```

Operator 通过公共契约接入，不导入 server 的 repo/model，也不直接写聊天数据库或 Redis。
Harness Adapter 在 Operator 进程的 runtime 一侧装配，server 不执行 Adapter。业务自有数据库、
API 和领域 CRD 可以通过普通 Client 访问，无需先进入 loopd Core。

## 接入与运行流程

1. Operator 入口创建 controller-runtime Manager 与 loop-runtime，注入 Harness Adapter、连接
   配置和日志，并负责关闭它们。runtime 本身不启动另一套 Manager。
2. 注册 Operator 的在线身份，让用户可以在 Chat 中选择它。
3. 通过 `Loop.Task.Watch` 将自身目标绑定到 Manager 下的 Reconciler，再启动 Manager。
   Reconciler 根据 Task 名称调用 `Loop.Task.Get`，获得本次问答上下文。
4. Reconciler 读取领域状态与外部事实，通过 Harness 等能力推进工作，发布可见过程；需要等待
   时选择等待 Call 或返回 Controller，并为再次调谐安排触发来源。
5. Operator 判断本次问答已完成后发布最终回答，调用 `Chat.Complete` 请求 server 完成交付。

## 注册与发现

注册与发现是 runtime 的一项协作能力。Operator 使用 `Loop.Operator.Register`，可直接接收
Task 的 Harness 使用 `Loop.Harness.Register`；Human 无需注册，也不进入可选目标列表。

runtime 首次注册成功后随自身生命周期定期续租，续租间隔为请求租期的三分之一。server 保存
Operator/Harness 各自的注册记录，通过未过期租约判断目标是否在线；停止续租后，目标自然从
发现列表消失。注册调用与后台续租使用不同的 context，结束首次请求不结束 runtime 的续租。

`operators`、`harnesses` 分别持久化逻辑目标的稳定 key、UUIDv7 主键、展示名称、描述、到期时间
及创建/更新时间；具体数据库字段由各自 model 定义。注册记录只承载发现信息，不承载领域配置
或调用轨迹。多个副本以同一 key 续租同一条记录，进程退出不主动删除，避免一个副本移除其他副本
的可用性；过期记录保留，供后续注册复用。

server 通过 `GET /v1/actors` 聚合在线 Operator 与 Harness，Chat UI 用它展示可选目标。
用户每次发送问题时选择 target 的 kind/key，Conversation 不绑定固定执行者。Operator 内部
临时创建、只能经由 Operator 调用的 Harness 不注册为直聊目标；Router 就只注册自身。

在线租约只说明近期有进程续租，不能保证发现后的请求立即得到处理，也不提供任务锁或多个副本
之间的执行互斥。Task 独立承担持久唤醒；副本协调由 Operator 使用 controller-runtime 等运行
机制配置。注册一个 Harness 不会自动安装 Adapter 或启动 Task 消费，执行目标仍需完成上下文
读取、执行和结果发布的接入。

## Task 与上下文

Task 是一次问答的公共路由入口，名称就是 task_id。简单 Operator 直接 Reconcile Task；复杂
Operator 可以由 Task 建立自己的领域 Resource，再用 controller-runtime 观察领域对象及其依赖。
领域 Resource 保存目标、进度和完成条件；Task 不复制 Query、完整 History 或高频流式事件。

`Task.Watch` 负责按目标过滤并接入 Controller。事件只负责唤醒，同一对象的多次变化可以合并，
Reconcile 必须重新读取事实。完成后删除 Task 的事件不作为新工作入队，避免把仍然存在的聊天
记录再次当成待执行请求。

`Task.Get` 通过 server 解析当前 input、response、Conversation 和截至 input 的有界 History。
Task 可能先于聊天事务提交可见，此时上下文暂时不存在，Reconciler 应返回可重试结果，不能将其
解释为业务失败。提交窗口与服务端上下文组装规则见 [Task 交付](../server/docs/task-delivery.md)。

Conversation 的历史跨 Operator/Harness 共享。`Chat.Conversation`、`Chat.History` 提供显式读取
入口；需要更早消息时按返回的水位和截断信息补读，需要固定长期输入时由领域 Resource 保存相关
Message 引用。内存中的 TaskContext 不承担领域 checkpoint，也不能替代外部系统的当前事实。

`Task.Messages(ctx, taskID, after, limit)` 分页读取这次任务的全部可见 Message，包含主会话中的
输入、回答和 Human 交互，以及工作会话中的协作输出；每条结果保留自己的 conversation_id。
`Chat.History` 只读取指定 Conversation，不递归合并工作会话，主会话历史可以跨多个 Task。
二者都是当前持久消息的读取，不是 AgentLedger 完整执行轨迹，也不隐式等待消息完成。
按 ID 翻页不负责感知已读消息后续修订；观察实时变化使用 Chat 流，读取 Human 权威状态使用 handle.Get。

Reconcile 用 Task ID 读取事实后发起 Effect，无需额外 Task 表。当前问题仍按显式 input 身份
定位，不能把后来的 user 回复当作原始问题；固定输入上下文用 Task.Get 的历史截止点，后续交互
使用 Task.Messages 或 Human handle 读取最新状态。

## 当前提供的 Effect Action

Effect Action 指通过 runtime 发起执行或改变外部协作状态的动作。当前 Reconcile 推进一次
问答的主要动作是调用 Harness、发布可见内容和完成问答；各动作使用自己的身份与重试契约。

| Action | 作用 | 身份与重试边界 |
|---|---|---|
| `Loop.Harness.Prompt` | 发起或复用一次 Harness 执行，返回 Call handle | `task ID + effect key`；同参数复用、参数变化冲突；跨重启去重由持久 Adapter 保证 |
| `Loop.Chat.Emit` | 发布进展、工具状态、产物引用或最终回答内容 | 按 Task 与 AgentUE seq 保持发布幂等；重新分配 seq 表示新事件，本地序号不提供跨重启保证 |
| `Loop.Chat.Complete` | 以成功或失败收口本次问答，请求固化回答、结束流并删除 Task | 按 task_id 重试同一次完成；发布内容与完成交付是两个动作 |

runtime 还提供以下会改变协作状态的 API，分别用于会话创建、问答入口和运行期注册：

| Action | 作用 | 身份与重试边界 |
|---|---|---|
| `Loop.Chat.CreateConversation` | 创建 User Conversation，或关联 Task 的工作会话 | 每个 Task 最多一个工作会话，重复创建返回冲突；不带 Task ID 每次创建新主会话 |
| `Loop.Chat.Send`（不带 task_id） | 提交问题，创建 Message 与 Task，并返回观察流 | 每次提交创建新问答；取得 task_id 后，用该 ID 续接观察 |
| `Loop.Operator.Register` / `Loop.Harness.Register` | 注册可接收 Task 的目标，并启动后台续租 | 按参与者类型与稳定 key 更新同一注册记录；续租随 runtime 生命周期运行 |

`Task.Get`、`Task.Messages`、`Chat.Conversation`、`Chat.History`、Call 的 `Value/Stream/Wait` 以及带 task_id 的
`Chat.Send` 都是读取或观察已有工作。`Task.Watch` 配置 Controller 的唤醒入口。这些能力不会
发起新的业务执行，不作为 Effect Action。

面向 Human 的 Effect Action 如下，输入、决议与恢复规则见后文“Human Effect：Ask 与 Confirm”。

| Action | 作用 | 身份与重试边界 |
|---|---|---|
| `Loop.Human.Ask` | 请求 Human 选择一个选项或提供自由文本 | `task ID + effect key` 复用问题 Message；同参数返回原问题或原结果，参数变化冲突 |
| `Loop.Human.Confirm` | 请求 Human 明确接受或拒绝所描述的事项 | 同一问题保持原 deadline 与结果；拒绝、忽略和超时均由 Operator 处理 |

Harness 与 Human 调用显式接受 EffectKey；runtime 没有通用的持久 Effect 引擎。Operator
直接调用业务 API 时，幂等、结果核对与补偿由 Connector 和外部系统承担。Harness 取消和外部
系统动作封装仍不在本次设计范围。

## Harness Call 与 Effect identity

`Loop.Harness.Prompt` 返回 Call handle。Operator 选择 prompt、tools 和目标，Adapter 将公共调用
转换成 Harness 协议；模型协议角色与 provider 特有状态止于 Adapter。

这是一项具有身份的外部动作：同一 Task 的同一逻辑步骤必须复用稳定的 EffectKey。同一 runtime
内，`task ID + effect key` 相同且参数相同的请求复用一个 Call；参数变化必须冲突。业务决定执行
新一轮动作时，应为新步骤分配可恢复的身份，不能在每次 Reconcile 中随机换 key 来绕过去重。

runtime 的 Call 表只在进程内。生产 Adapter 必须将相同 IdempotencyKey 映射到同一个持久执行，
在 Operator 重启后恢复或观察它；当前内置 AgentGo Adapter 是进程内 demo，不提供这项保证。
持久 Resource 与稳定 key 都是恢复所需条件，单靠重新运行 Reconcile 无法避免外部重复执行。

### 等待、重试与恢复

Call 可以通过 `Value` 观察当前状态、通过 `Stream` 消费已观察的事件，或通过 `Wait` 等待终态。
流式事件只表示有进展，成功与失败由终态表达。`Wait` 的 context 结束只结束这次等待，不等于取消
Harness 执行；本地调用与观察的生命周期由 runtime 管理，持久执行仍归外部 Harness。

当剩余工作只有等待结果时，可以在 Reconcile 内调用 `Wait`，但它会占用 Controller 的一个并发位。
需要释放该位置时，Reconciler 可以观察当前状态后返回 `RequeueAfter`；有领域资源变化时，也可以
通过自己的 Watch 再次唤醒。当前 Task watch 不会自动将 Harness 完成事件映射成 Reconcile 请求，
不能返回空结果后假定结果到达就会自动继续。Call 的本地 Stream 也不是持久事件订阅。

controller-runtime 根据 Reconcile 返回的 error 或 Result 处理调度。Operator 判断某次失败应重试、
调整计划、换一个动作还是结束问答；runtime 提供调用结果，不替业务作完成决定。进程重启后从
Resource 与事实 owner 重新构造工作，不恢复 Go 调用栈、goroutine 或本地 handle。

## Human Effect：Ask 与 Confirm

`Loop.Human` 是面向人的能力分组，与
`Loop.Harness` 并列。Operator 提问、用户答复直接记录为 Message；runtime 以问题 Message
为身份等待结果，Operator 拥有问题的业务含义、并行关系和后续动作。

### 输入、超时与结果

两项 Action 都必须提供 TaskID、EffectKey、Title、Prompt 和有限正 Timeout（Go time.Duration，JSON 使用纳秒）。用户不理会问题
是正常情况，因此 Timeout 必填，不能省略、设为零或无限等待。它限定等待人的总时长，独立于
HTTP timeout 与本地调用 context；server 首次持久化问题时确定 deadline，重试与重启不重置它。

| Action | 特有输入 | success 的业务值 |
|---|---|---|
| `Human.Ask` | Choices：稳定 value、label、可选 description；AllowOther 决定是否接受自由文本 | 选中的稳定 value，或非空自由文本 |
| `Human.Confirm` | 可选 ConfirmLabel、DeclineLabel，只改变展示文案 | `accepted` 或 `declined` |

Ask 的 options 通过 Choices 声明。每个选项的 value 是返回给 Operator 的稳定值，label 是
用户可读文案，description 可选；value 必须非空且唯一，label 必须非空。

| Ask 模式 | Choices | AllowOther | 允许的回答 |
|---|---|---|---|
| 封闭选项 | 提供非空选项列表 | false（默认） | 单选一个已声明的 value |
| 选项加自由文本 | 提供非空选项列表 | true | 选择一个 value，或输入非空自由文本 |
| 纯自由文本 | 不提供选项 | true | 非空自由文本 |

例如范围选择可以声明以下选项；它们也对应 ask block 的 choices 与 allow_other：

```text
Choices: [
  { Value: "minimal", Label: "最小范围", Description: "只处理当前问题" },
  { Value: "full", Label: "完整范围", Description: "同时处理相关问题" }
]
AllowOther: true
```

AllowOther 为 false 时缺少选项、显式提供空列表、重复 value 或提交未声明的 value 均应拒绝。
自由文本模式的返回值可能不在 choices 中，不能向调用方承诺仅返回选项枚举。

Confirm 固定提供接受和拒绝两个决议，可用 ConfirmLabel/DeclineLabel 定制按钮文案；返回值始终
是 accepted/declined。需要任意多个业务选项时使用 Ask。两类交互都另外提供“忽略/取消”，返回
独立的 dismissed；它不是一个 choices value，也不等同于 Confirm 的 declined。

Action 创建或复用问题 Message，返回以其 ID 标识的 typed handle；`Get(ctx)` 读取权威请求
状态，`Wait(ctx)` 等待终态。请求由 pending 进入且只能进入一个不可变终态：

| 终态 | 含义与 Operator 处理 |
|---|---|
| `success(value)` | 用户明确回答；Confirm 接受或拒绝均是正常业务值 |
| `dismissed` | 用户主动“忽略/取消”，或执行等价于 Baton Esc 的操作；Operator 采取相应兜底 |
| `timeout` | 用户一直未答，持久 deadline 到期；Operator 采取无人答复时的兜底 |
| `failure(reason)` | 平台明确终止请求，例如所属 Task 以失败结束；Operator 处理报告的失败 |

`dismissed/timeout` 是普通结果，不作为调用 error，不自动使 Task 失败、取消整个任务或重新
提问。Operator 可以使用默认值、跳过步骤、提供替代方案或结束任务；Confirm 只有
`success(accepted)` 表示同意，兜底不能把忽略、超时或拒绝当成授权。

参数错误、身份冲突和网络读取失败通过调用 error 返回。`Wait` 的 context 取消仅结束本地
等待，关闭浏览器或断线也不等于显式忽略；问题仍待答，直到用户操作、deadline 到期或平台终止。
超时由 server 根据持久 deadline 推进，不依赖浏览器、Operator 或等待 goroutine 存活。

### Message 与并行交互

Message 记录 Actor 发出了什么消息；记录的时间顺序不定义执行顺序。`reply_to_message_id`
只表达“回答哪个 Message”，不承担队列、执行依赖或串行调度。Operator 可以先发起多个 Human
请求和 Harness Call，再分别观察结果；同一 Conversation、同一 Task 中可以同时存在多个待答问题：

| Message | Actor | 内容 | reply_to_message_id |
|---|---|---|---|
| m1 | user | 发起工作 | 空 |
| m2 | operator | Ask：确认范围 | m1 |
| m3 | operator | Confirm：确认预算 | m1 |
| m4 | user | accepted，先答预算 | m3 |
| m5 | user | 范围说明，后答范围 | m2 |
| m6 | operator | 主回答 | m1 |

每个问题具有独立 EffectKey、deadline、状态和答复。一个问题 pending、dismissed 或 timeout
不阻塞另一个问题、Harness Call 或其他 Task，也不自动把整个 Conversation 标记成等待。
Operator 决定哪些结果是后续工作的必要条件；在 Reconcile 内选择 `Wait` 会占用当前并发位。

回复的唯一关联依据是 `reply_to_message_id`，不能按前后挨着、时间顺序、最近的待答问题、
Actor 或 task_id 推断。引用缺失时不自动配对；Human 答复必须提供引用，目标不存在或越界则拒绝。
消息分页、交错展示和乱序到达都不改变这一契约。

回复关系限制在同一 Conversation 与 Task 内；普通消息可以有多条回复，Human 问题只接受一次
有效终态答复。主 input/response 保持创建时确定的身份，不能按最后一条 user/非 user 消息猜测，
也不能将后到的反问或答复替换成原始问题。

### 用 AgentUE block 表达交互

沿用 `Message.content` 的 AgentUE JSON model。loopd 在业务层声明以下 block type，AgentUE
继续提供通用 `start/set/append` 与 Reducer；这些类型不增加新的 AgentUE op：

| block type | Actor 与语义 | 主要字段 |
|---|---|---|
| `ask` | Operator 提问 | title、prompt、choices、allow_other、status、deadline |
| `confirm` | Operator 请求确认 | title、prompt、confirm_label、decline_label、status、deadline |
| `human_reply` | User 回答或主动忽略 | outcome：success/dismissed；success 时携带 value |

每条提问 Message 承载一个待答的 ask 或 confirm block，Title/Prompt 提供说明；多个
并行问题用多条 Message 表达。答复通过消息级 reply_to_message_id 关联问题，无需额外 block
级回复关系。每条 Message 独立保存 model，block ID 的唯一范围是所属 Message。

问题的 EffectKey、请求指纹等控制信息放在受控的 `content.meta` 扩展，状态与 deadline 由问题
block 保存；答复值保存在 user Message 的 human_reply block。server 管理这些字段的写入与
原子收口，不另存一份 Interaction 问题和答案。超时、平台失败只更新问题状态，不伪造 user 消息；
dismissed/timeout 也不使用 AgentUE `error` 或 `meta.error` 把正常结果渲染成执行异常。

Web 在主会话按 block type 展示问题卡、输入或选项以及忽略操作；已结束的问题展示结果并停止
接收答复。普通 `Chat.Emit` 不得伪造 human_reply、修改不可变问题或直接批准请求。Web 为这些业务 block 提供问题卡、选项、自由输入和独立取消入口，终态卡不再接收输入。

### 幂等、发布与唤醒

同一 Task 的 Human Action 共享 EffectKey 命名空间。server 原子创建或读取问题 Message，比较
动作类型、问题、选项和 Timeout 等不可变输入；同 key 同参数返回原问题及结果，变化则冲突。
已忽略或超时的问题不被重开；Operator 决定再次提问时使用新的步骤身份。

typed reply 入口接收问题 Message ID 和 answer/dismiss，server 校验可信回答者身份、访问范围、
待答状态与答案类型，在同一事务中创建 user Message 并收口问题状态。同一答复重试返回已有记录，
矛盾答复冲突；答复与 deadline 竞争时按 server 时间原子判定，迟到答复不能覆盖终态。
`Chat.Send(task_id)` 继续只负责观察，单纯续接不构成回答。

问题落库即能展示，答复落库即能展示，均无需等待 Task 完成。页面更新必须定位目标 Message，
不能将并行问题与答复的 block 折叠到同一个主 response。loopd 的交付封装携带 message_id，内层
保持标准 AgentUE event；每条 Message 分别维护 model 与语义序号，Task 只聚合分发。Human Message 使用独立 revision，重连发送当前快照；主回答继续使用现有 Redis 交付。

答复、忽略或超时先持久化请求终态，再发布目标消息更新并推进 Task revision。数据库与 Kubernetes
不共享事务，server 在 Message 上持久记录待交付通知并重试；并行终态通知可以合并唤醒，Reconciler 每次读取
各问题的权威状态。内容不写入 Task CRD，投影或 Redis 丢失后从 Message 恢复。

Operator 可以返回等待 Task 变化或使用 `RequeueAfter`；复杂 Operator 将变化映射到领域 Resource。
重启后以原 EffectKey 取回结果，并核对领域对象 UID、计划版本和前提。Baton 保留 live execution
continuation；loopd 通过持久 Message 重新调谐，进程消失本身不让请求进入 failure。

交互创建、答复与 Task 收口需协调并发。正常完成前仍有 pending 问题时，`Chat.Complete` 返回
冲突；dismissed/timeout 已是终态，Operator 可以完成兜底后正常收口。Task 失败结束前，将剩余
pending 问题标成 `failure(task_ended)`；结束后禁止新问题或迟到答复重新唤醒 Task。

### 接入示例与身份边界

```go
request, err := loop.Loop.Human.Ask(ctx, runtime.AskRequest{
    TaskID: task.ID, EffectKey: "scope/v1",
    Title: "确认范围", Prompt: "本次要处理哪些内容？", Timeout: 5 * time.Minute,
    Choices: []loopd.HumanChoice{
        {Value: "minimal", Label: "最小范围"},
        {Value: "full", Label: "完整范围"},
    },
    AllowOther: true,
})
if err != nil { return reconcile.Result{}, err }
result, err := request.Get(ctx)
if err != nil { return reconcile.Result{}, err }
if result.Status == loopd.HumanPending {
    return reconcile.Result{RequeueAfter: time.Minute}, nil
}
// 根据 success/value、dismissed、timeout 或 failure 推进领域状态。
```

HTTP 创建入口是 `POST /v1/tasks/:task_id/human`，读取入口是 `GET /v1/human/:message_id`，
答复入口是 `POST /v1/conversations/:conversation_id/tasks/:task_id/replies`。答复体显式携带
reply_to_message_id、outcome 与可选 value，不通过 Chat.Send 或普通 Emit 表达批准。

Quick Start 为浏览器签发不透明 HttpOnly Cookie，以其摘要作为 user Actor key；同一浏览器能
回答自己发起的 Task，提交任意 user_key 不会改变身份。托管方可用 server.Config.HumanIdentity
接入可信登录身份，创建问题和答复必须使用同一身份解析规则。该身份契约只约束 Human 答复；
现有 Operator 写入、发现和历史读取 API 仍沿用可信部署边界，不提供完整多租户访问控制。

server.Run 负责无人在线时的 deadline 与通知重试，必须随服务进程启动。TaskClient 的 Exists
用于拒绝向已退休问答追加新问题，Wake 只增加现有 Task revision；部署 RBAC 需允许更新 Task。

### 实现验收场景

以下 Case 由 Go 单元／集成测试及 Web 类型、投影和渲染测试覆盖：

- Timeout 缺失、非正或无界时拒绝创建；无人答复返回 timeout，主动忽略返回 dismissed，均可兜底。
- 重复调用复用原问题、原 deadline 与终态；断线不算忽略，超时不伪造 user Message。
- 并行 Ask/Confirm 可乱序答复并各自收口；一个问题超时不阻塞其他执行或覆盖原始 input/response。
- 答复即使紧挨另一个问题也只关联 reply_to_message_id；缺失或无效引用不能按位置自动补全。
- Ask 的封闭选项、选项加自由文本、纯自由文本三种模式均按声明校验；非法或重复选项被拒绝。
- Confirm 修改按钮文案后仍返回 accepted/declined；拒绝为 success(declined)，忽略为 dismissed。
- 自由文本返回值不受选项枚举限制；忽略和超时不被当成有效选项或 Confirm 同意。
- 多条消息使用相同 block ID 仍独立更新；ask/confirm/human_reply 可展示，正常结果不显示为 error。
- 重复、矛盾或迟到答复只产生一个终态；越权或跨 Conversation/Task 的答复被拒绝。
- 重启、投影或唤醒失败后恢复原消息与通知；Operator 正常结束兜底后，迟到回复不能复活 Task。

## 可见过程与回答

`Chat.Emit` 发布 AgentUE 可见事件，`Chat.Complete` 请求固化回答并完成交付。内部 Harness 可以
贡献工具状态和处理详情，主回答由用户选中的 Operator 汇总。工作 Conversation 关联本次 Task，
其聊天存储关系见 [聊天持久化](../server/docs/persistence.md)。

当前实现中，同一 runtime 对同一 Task 的并行发布串行分配 AgentUE seq，用于语义顺序和发布幂等；
它不是多个独立 runtime 的全局分配器，也不在进程重启后自动恢复。Redis/SSE Event ID 则是续接
传输位置，不能拿来替代 seq 或 Harness EffectKey。

`Chat.Complete` 成功意味着 server 已完成回答交付；失败时调用方保留同一 Task 重试完成。
server 负责固化 Message、终结 Stream、删除 Task 的顺序与补偿，runtime 不直接操作这些存储。
完成契约与页面续接协议见 [Task 交付](../server/docs/task-delivery.md)。

页面事件只承载可见信息，完整 prompt、模型事件、工具调用、重试与成本属于执行轨迹。AgentLedger
是这类事实的承载边界；当前 runtime 尚未接入完整轨迹记录，`Chat.Emit` 不构成审计保证。

## 用业务代码表达编排

Router 直接处理 Task：读取 Query/History，按 `plan`、`work/<index>`、`summarize` 三类稳定 key
规划、并行发起子任务并汇总。当前阶段之间用 `Wait` 串联，调用配置的临时 Harness，不从注册表
预留执行者。更复杂的 Operator 可以用领域 Resource 保存计划与验证状态；角色、依赖和完成条件
属于业务，公共协作能力由 runtime 提供。

## 实现与验证入口

[Runtime](../runtime/runtime.go) 装配 [Task](../runtime/task.go)、[Harness](../runtime/harness.go)、
[Chat](../runtime/chat.go) 、[Human](../runtime/human.go) 与 [Registry](../runtime/registry.go)；执行扩展点见
[Adapter](../harness/harness.go)，接入示例见 [Router](../operators/router/internal/router/router.go)。
现有回归入口是 `runtime/*_test.go`、`harness/agentgo/adapter_test.go` 和 Router 测试，
服务端交付规则由 server 测试维护；Human Effect 的验收场景见上节。
controller-runtime 的参照依据是其 [包总览](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg)
中的 Manager、Controller、Reconciler 与 Watch 分工。
