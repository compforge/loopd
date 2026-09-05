# Loop Runtime

loop-runtime 是嵌入 Operator 进程的 Go 协作 toolkit。它在 controller-runtime 的控制循环之上，
提供接入 loopd、读取协作上下文、调用 Harness 和发布结果的类型化 Verb，使业务可以用普通
Reconcile 代码实现自己的编排。本文是这些开发契约的唯一设计入口；跨组件事实归属见
[Kernel](kernel.md)。

## 相对 Operator 的定位

controller-runtime 为 Kubernetes Controller 提供 Manager、Client、Cache、Watch 与队列，
Operator 提供 Resource 和 Reconciler 中的业务逻辑。loop-runtime 沿用这种库与业务的关系：
它沉淀多个 Operator 都需要的协作机制，Operator 决定目标意味着什么、下一步做什么以及何时完成。

两者在同一 Operator 内组合。controller-runtime 负责资源控制循环，loop-runtime 补充 Human、
Conversation 与 Harness 的协作能力；已有的 Manager、调度队列、重试和资源 Client 直接复用。
Verb 是 Reconcile 读取协作事实、推进实际工作的能力入口，不替代 Reconcile 的业务判断。
“Loop is a CRD” 的落地既需要状态收敛结构，也需要这些可执行的协作能力，而不只是资源 CRUD。

| 关注点 | controller-runtime | loop-runtime | Operator |
|---|---|---|---|
| 进程与调度 | Manager 启停 Controller，提供共享依赖、队列与并发控制 | 管理协作 Client、注册续租与本地 Call 观察 | 组装两套库，配置运行范围与生命周期 |
| 唤醒与读取 | Watch 资源，将变化映射成 Reconcile 请求；Client 读取当前状态 | 将参与者的 Conv 信号接入 Controller，解析会话上下文 | 定义领域 Resource、Watch 关系，读取外部业务事实 |
| 推进工作 | 调用 Reconciler，按其返回值重试或再次排队 | 提供 Harness Call、可见过程和回答发布契约 | 决定拆解、串并行、工具、角色与完成条件 |
| 状态与恢复 | 根据资源当前状态重新调谐 | 保持协作引用和动作身份，连接各事实 owner | 保存领域进度，重新读取事实后决定如何继续 |

```text
Operator process
  ├─ 领域 Resource / Reconciler / 外部系统 Client
  ├─ controller-runtime：Manager / Controller / Watch / Client
  │                         └─ Kubernetes API
  └─ loop-runtime
       ├─ Chat / Conv / Context / Registration ── loop-server
       ├─ Conv.Watch ── controller-runtime
       └─ Harness.Prompt ── Adapter ── Harness
```

Operator 通过公共契约接入，不导入 server 的 repo/model，也不直接写聊天数据库或 Redis。
Harness Adapter 在 Operator 进程的 runtime 一侧装配，server 不执行 Adapter。业务自有数据库、
API 和领域 CRD 可以通过普通 Client 访问，无需先进入 loopd Core。

### Verb 背后的公共机制

Verb 是 Operator 面向协作基础设施的入口，底层机制由 runtime 与 server 配合承担，而不是
要求每个 Operator 自己实现一套数据访问、流式交付和交付重试逻辑：

- 数据读取：按 Chat 交付 ID 或 Conversation 获取消息与上下文，屏蔽聊天存储和服务端组装细节。
- 输出交付：runtime 按 Message 发布流式输出，server 协调事件传输与消息固化，再通过
  AgentUE 交付前端；Operator 不需要感知页面连接，也不直接操作聊天数据库或 Redis。
- 扩展接入：业务通过 Operator 扩展编排策略，Harness 通过 Adapter 接入，差异不进入聊天核心。
- 交付可靠性：公共层提供消息续接、交付收尾重试及各 Verb 约定的幂等能力。流式续接
  不等于执行恢复，也不意味着每个增量都已固化到数据库。

执行恢复不由 runtime 的内存状态保证。编排层由 Operator 将领域进度与稳定动作身份持久化到
CRD，重启后通过 Reconcile 重新读取并推进；Harness 层由 Adapter 及其执行端负责，loopd 只按
契约接入，不实现 Harness 内部恢复。agentd 承担持久执行，agentgo 只是进程内模拟。

Operator 需要理解这些调用契约和恢复边界，但无需关注其存储、传输与前端交付的实现细节。

## 接入与运行流程

1. Operator 入口创建 controller-runtime Manager 与 loop-runtime，注入 Harness Adapter、连接
   配置和日志，并负责关闭它们。runtime 本身不启动另一套 Manager。
2. 注册 Operator 的在线身份，让用户可以在 Chat 中选择它。
3. 通过 `Loop.Conv.Watch` 将自身目标绑定到 Manager 下的 Reconciler，再启动 Manager。
   Reconciler 根据 Conv 名称调用 `Loop.Conv.Listen`，获得发给自己的新消息。
4. Reconciler 读取领域状态与外部事实，通过 Harness 等能力推进工作，发布可见过程；需要等待
   时选择等待 Call 或返回 Controller，并为再次调谐安排触发来源。
5. Operator 判断本次问答已完成后发布最终回答，调用 `Chat.Complete` 请求 server 完成交付。

## 注册与发现

注册与发现是 runtime 的一项协作能力。Operator 使用 `Loop.Operator.Register`，可直接接收
消息的 Harness 使用 `Loop.Harness.Register`；Human 无需注册，也不进入可选目标列表。

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
之间的执行互斥。Conv CRD 独立承担持久唤醒；副本协调由 Operator 使用 controller-runtime 等运行
机制配置。注册一个 Harness 不会自动安装 Adapter 或启动 Conv 消息消费，执行目标仍需完成上下文
读取、执行和结果发布的接入。

## Conv、Listen 与上下文

Conv CRD 是协作边界，名称等于 Conversation ID。每位参与者分别拥有最新消息信号和 Listen
游标。信号只负责唤醒，Listen 仍按 DB 查询新消息，不把信号作为读取上限。

`Conv.Listen(ctx, convID, ListenRequest{Actor: actor, Limit: limit})` 是 write Verb，返回定向或显式
广播的新消息，并推进该参与者的游标。Operator 自己决定何时 Listen、如何合并发言或启动新工作。
`Conv.Read` 是 read Verb，只读共享历史，不改变游标、不自动调度其他参与者。

`Chat.Context(ctx, taskID)` 读取某次页面交付的原始输入、目标及有界历史，默认回答可以不存在。
`Chat.Messages` 分页读取该交付全部可见消息；`Chat.History` 只读取指定会话，不递归合并详情。
这些读取不是完整执行审计，也不是 Operator 领域 checkpoint。

Listen 游标确认接收而非完成。HTTP 响应丢失可能发生在游标提交之后，不能把再次 Listen 当作
业务执行重试；Operator 可 Read 找回事实，需要持久执行时自行保存领域状态。
UUIDv7 顺序、并发和恢复边界见 [Conversation](../server/docs/conversation.md)。

## Verb 与 Effect

runtime 提供给 Operator 的协作操作统一称为 **Verb**。Verb 表达“可以做什么”，其 **Effect**
按调用者意图分为 `read` 和 `write`，表达“读取已有事实，还是发起工作或改变协作状态”：

- **Effect = read**：读取、等待或订阅已有事实，不发起新的业务工作。订阅建立本地观察，
  服务端刷新到期状态或重建交付投影，不改变它的读取意图。
- **Effect = write**：发起执行，或创建、发布、收口和维护协作事实。每个 Verb 分别声明身份、
  冲突和重试规则；write 不意味着统一幂等，也不意味着自动持久恢复。

### Effect = read 的 Verb

| 能力 | 操作与读取边界 |
|---|---|
| Conv | `Read` 读取共享消息；`Watch` 订阅参与者信号 |
| Chat | `Context/Messages` 按交付 ID 读取；`Conversation/History` 读取会话；带 task_id 的 `Send` 续接 |
| Harness Call | `Value/Stream/Wait` 观察已有执行；本地观察不是持久订阅 |
| Human handle | `Get/Wait` 读取或等待权威问题状态；停止等待不等于忽略问题 |

### Effect = write 的 Verb

| Verb | 身份与重试边界 |
|---|---|
| `Conv.Listen` | DB 新消息按 ID 排序，非空批次推进当前参与者 CRD 游标；接收不等于执行完成 |\n| `Harness.Prompt` | task ID + EffectKey；同参数复用，变化冲突，跨重启去重由持久 Adapter 保证 |
| `Human.Ask/Confirm` | task ID + EffectKey；复用原问题、deadline 和结果，变化冲突 |
| `Chat.Output` | task ID + Key；创建或复用工作会话中的 Message，发送者不可变，同一发送者可有多条输出 |
| `Chat.EmitMessage` | 按 Message 发布；该 Message 内的 seq 保持幂等，block ID 不跨消息唯一 |
| `Chat.Emit` | 首次有效发布时懒创建默认回答；与显式 EmitMessage 不混用本地序号分配 |
| `Chat.Complete` | 按 task_id 重试同一收口意图；固化所有输出并完成任务，不由某条消息的 end 自动触发 |
| `Chat.CreateConversation` | 每个 Task 最多一个工作会话，重复创建冲突；不带 Task ID 每次创建新主会话 |
| 不带 task_id 的 `Chat.Send` | 每次提交创建新工作；取得 task_id 后可续接观察 |
| `Operator.Register/Harness.Register` | 按参与者类型与稳定 key 更新注册，后台续租随 runtime 生命周期运行 |

Effect 的 read/write 分类不增加统一 Verb/Effect CRD 或执行引擎，也不要求在 API 中增加一层
`Verbs` 容器；`Loop.Conv`、`Loop.Chat`、`Loop.Human` 等仍按协作对象组织 Verb。
Verb 名称不是调用身份：例如 `Harness.Prompt` 是 Verb，`plan`、`work/0` 是任务内的步骤 key；
`EffectKey` 继续用于需要稳定调用身份的操作。Operator 直接访问业务 API 时，
幂等、结果核对与补偿仍由相应 Connector 和事实 owner 承担。

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
通过自己的 Watch 再次唤醒。Conv watch 不会自动将 Harness 完成事件映射成 Reconcile 请求，
不能返回空结果后假定结果到达就会自动继续。Call 的本地 Stream 也不是持久事件订阅。

controller-runtime 根据 Reconcile 返回的 error 或 Result 处理调度。Operator 判断某次失败应重试、
调整计划、换一个动作还是结束问答；runtime 提供调用结果，不替业务作完成决定。进程重启后从
Resource 与事实 owner 重新构造工作，不恢复 Go 调用栈、goroutine 或本地 handle。

## Human Verb：Ask 与 Confirm

`Loop.Human` 是面向人的能力分组，与
`Loop.Harness` 并列。Operator 提问、用户答复直接记录为 Message；runtime 以问题 Message
为身份等待结果，Operator 拥有问题的业务含义、并行关系和后续动作。

### 输入、超时与结果

两项 Verb 的 Effect 都是 write，必须提供 TaskID、EffectKey、Title、Prompt 和有限正 Timeout（Go time.Duration，JSON 使用纳秒）。用户不理会问题
是正常情况，因此 Timeout 必填，不能省略、设为零或无限等待。它限定等待人的总时长，独立于
HTTP timeout 与本地调用 context；server 首次持久化问题时确定 deadline，重试与重启不重置它。

| Verb | 特有输入 | success 的业务值 |
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

Ask/Confirm 创建或复用问题 Message，返回以其 ID 标识的 typed handle；`Get(ctx)` 读取权威请求
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
有效终态答复。input 与实际发布的 response 各有显式身份，不能按最后一条 user/非 user 消息猜测，
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

同一 Task 的 Human Ask/Confirm Verb 共享 EffectKey 命名空间。server 原子创建或读取问题 Message，比较
动作类型、问题、选项和 Timeout 等不可变输入；同 key 同参数返回原问题及结果，变化则冲突。
已忽略或超时的问题不被重开；Operator 决定再次提问时使用新的步骤身份。

typed reply 入口接收问题 Message ID 和 answer/dismiss，server 校验可信回答者身份、访问范围、
待答状态与答案类型，在同一事务中创建 user Message 并收口问题状态。同一答复重试返回已有记录，
矛盾答复冲突；答复与 deadline 竞争时按 server 时间原子判定，迟到答复不能覆盖终态。
`Chat.Send(task_id)` 继续只负责观察，单纯续接不构成回答。

问题落库即能展示，答复落库即能展示，均无需等待 Task 完成。页面更新必须定位目标 Message，
不能将并行问题与答复的 block 折叠到同一个主 response。loopd 的交付封装携带 message_id，内层
保持标准 AgentUE event；每条 Message 分别维护 model 与语义序号，Task 只聚合分发。Human Message 使用独立 revision，重连发送当前快照；主回答继续使用现有 Redis 交付。

卡片答复先持久化请求终态与 user Message，再通过 Conv 通知原提问者；通知失败由后台重试。
超时只改变问题状态，不伪造消息。Operator 通过 handle.Get/Wait 或按 deadline 安排 RequeueAfter
读取结果，不假设每次状态变化都产生一条新消息。

创建、答复与 Chat 收口通过输入行锁协调。正常完成前仍有 pending 问题时，Chat.Complete 返回
冲突；失败收口将剩余问题标为 failure。结束后的交付不接受新问题或迟到答复，但 Conv 继续存在，
参与者可以继续发送新的 Chat 发言。

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

server.Run 负责无人在线时的 deadline、Conv 通知与交付收尾重试，必须随服务进程启动。
是否允许追加交互由 Chat 交付状态决定，不查询 Task CRD。

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

`Chat.Output` 为工作输出建立 Message，随后用 `Chat.EmitMessage` 向它发布 AgentUE 事件。
`Chat.Emit` 是懒创建默认回答并发布的便捷入口；发送者、block 内容和 Call ID 不决定投递目标。
`Chat.Complete` 请求固化全部输出并完成任务。内部 Harness 可以
贡献工具状态和处理详情，主回答由用户选中的 Operator 汇总。工作 Conversation 关联本次 Task，
其聊天存储关系见 [聊天持久化](../server/docs/persistence.md)。

当前实现中，同一 runtime 对同一 Message 的并行发布串行分配 AgentUE seq，用于语义顺序和发布幂等；
它不是多个独立 runtime 的全局分配器，也不在进程重启后自动恢复。Redis/SSE Event ID 则是续接
传输位置，不能拿来替代 seq 或 Harness EffectKey。

`Chat.Complete` 成功意味着 server 已完成回答交付；失败时调用方保留同一 Task 重试完成。
server 负责固化 Message、终结 Stream、标记交付关闭的顺序与补偿，runtime 不直接操作这些存储。
完成契约与页面续接协议见 [Task 交付](../server/docs/task-delivery.md)。

页面事件只承载可见信息，完整 prompt、模型事件、工具调用、重试与成本属于执行轨迹。AgentLedger
是这类事实的承载边界；当前 runtime 尚未接入完整轨迹记录，`Chat.Emit` 不构成审计保证。

## 用业务代码表达编排

Router 直接 Reconcile Conv，不创建 Work CRD。它先 Listen 一个输入，读取上下文并运行
plan → 有界并行 Harness → 汇总。当前批 Harness 完成后再次 Listen：若有新发言，带着累计结果
重新 plan，决定直接 summary，还是继续分派。汇总生成期间到达的补充也会在发布前触发重新计划。

每轮调用使用不同的稳定 EffectKey；一轮执行只发布一条主回答，所有被接收发言的 Chat 流均收尾。
这只是 Router 的业务策略，不是 runtime 为所有 Operator 定义的任务边界。当前使用 Wait，
不接入 steer/followup；这些能力由支持它们的 Harness Adapter 及 Operator 策略扩展。

Router 是单进程示例，接收后的循环状态位于内存，不宣称进程重启后自动恢复。
Conv 游标持久化不等于计划/结果持久化；需要跨重启保证时由业务保存领域进度。
所有 Harness 临时创建；注册 Harness 的选择、转发和分派策略由 Router 后续自行扩展。

## 实现与验证入口

[Runtime](../runtime/runtime.go) 装配 [Conv](../runtime/conversation.go)、[Harness](../runtime/harness.go)、
[Chat](../runtime/chat.go) 、[Human](../runtime/human.go) 与 [Registry](../runtime/registry.go)；执行扩展点见
[Adapter](../harness/harness.go)，接入示例见 [Router](../operators/router/internal/router/router.go)。
现有回归入口是 `runtime/*_test.go`、`harness/agentgo/adapter_test.go` 和 Router 测试，
服务端交付规则由 server 测试维护；Human Verb 的验收场景见上节。
controller-runtime 的参照依据是其 [包总览](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg)
中的 Manager、Controller、Reconciler 与 Watch 分工。
