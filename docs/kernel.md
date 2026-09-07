# loopd Kernel

loopd 是 Actor 通过持久消息协作的平台，也是 “Loop is a CRD” 在编排层的实现。
User、Operator 与 Harness 都是 Actor：各自运行，自己决定何时接收消息、何时回应，
处理到安全位置再确认消费。
Conversation 是共享交流空间，不是由一次请求驱动、等待一个答案后结束的固定工作流。

## 协作模型

Human 可以在 Operator 工作期间持续发言；Operator 可以先回应一部分，再继续工作和发言。
双方不互相占有生命周期，也不必轮流执行。Operator 通过 Poll 接收输入，通过 Speak 发给 User、其他 Operator 或会话中的 Actor，
在结果或领域进度足以支持安全恢复后 Commit；何时做这些事，由各 Operator 的业务决定。

Actor 模型也容纳直接面向用户的 Harness。角色描述身份，不表示谁是主、谁是辅；但统一身份不等于统一
执行协议：Operator 使用 Poll/Commit，Harness 的接入和执行恢复仍由 Adapter 契约负责。

协作所需的状态各有归属：

| 载体 | 责任 | 不承担 |
|---|---|---|
| DB | 保存 Conversation、Message 与 Harness 调用记录，提供消息快照和持久调用结果 | Operator 领域进度、Harness 原生执行状态、完整执行轨迹 |
| Conv CRD | 保存参与者过程会话关联、消费进度与定向信号，通过 Watch 触发 Reconcile | 消息正文、业务工作完成判定 |
| Redis | append-only保存实时事件，经 SSE 服务 UI 会话流与 Operator 调用流，支持重连与 replay | 参与者消费进度、业务执行恢复 |

可以把 DB 理解成协作 queue，但消费不会删除消息，也不争抢一个全局消费位置；参与者各自
维护消费进度，历史仍可 Read。Poll 表示收到，Commit 表示可安全越过该消费前缀，不表示整个
会话或 Operator 的工作结束。

这里参考 Kafka 的 Producer 与多 Consumer 模型：参与者发言时是 Producer，接收消息时是
Consumer，同一参与者可以兼具两种职责。不同 Actor 独立消费面向自己的消息、独立提交进度，
更接近不同 consumer group 各自消费，而不是同一 group 内竞争分配消息。loopd 借用这种
生产与消费解耦的协作语义，不实现 Kafka 的分区、消费者组协调协议，也不宣称具备其全部日志保证。

## 定位与边界

- loop-server 拥有可见 Conversation、Message、在线注册、消息交付和 Harness 调用的后台驱动。
- loop-runtime 是嵌入 Operator 的 Go SDK/toolkit，封装 Server API，并辅助接入 controller-runtime。
- Operator 决定业务含义、消息如何组成工作、何时接收补充信息及何时完成。
- Harness 通过 Adapter 提供智能执行；执行状态与恢复属于 Harness。
- AgentLedger 承载完整执行轨迹，不替代可见聊天记录。

公共协作模型和调用契约由 `pkg/contract`（`package contract`）定义，包含 Actor、Message、
Conversation、Human、Harness 调用及 Speak/Poll/Commit；`pkg/k8s/v1alpha1` 定义共享的 Conv CRD。
server、runtime 和 harness 使用同一份公共契约；CRD 可以依赖 contract，contract 不依赖 Kubernetes
或任何组件实现。`pkg/harness` 定义 Adapter 接口及执行句柄，依赖 contract；
调用由 runtime 提交，后台驱动和输出持久化由 Server 负责。页面 View 和持久化模型继续由 server 拥有。

Operator 不依赖 server 的私有 model/repo，不直接操作聊天数据库或 Redis。
server 不导入 Operator 领域 CRD；HarnessRunner 只驱动 Adapter 公共契约，不解释 provider 原生协议。

Operator 关注收发消息与业务逻辑。Server 负责消费进度持久化、通知重试、消息增量固化和流式
交付，runtime 将这些服务封装为 Verb 与可重建句柄，让业务代码不必管理数据库 queue、Redis
或 SSE 连接。消息发送成功以 DB 接收为准，流传输失败不改变这一事实；何时 Commit 仍由业务
安全边界决定。

## 参与者与会话

ActorKind 是开放字符串枚举，`user`、`operator`、`harness` 是内置常量。Operator 可以写入
扩展 kind，`operator/<operator-key>/<role>` 是已有的命名惯例，不是封闭的枚举或固定层级限制。
扩展 kind 约定沿用三个内置身份前缀，当前不强校验前缀；未知值原样读写。Actor 是这些身份的聚合概念，
以 kind/key 共同标识；`/actors` 只发现在线注册的 Operator/Harness，不枚举全部用户或自定义角色。
Message 的发送者和收件者各有 kind/key，
回复引用表达“回应哪条消息”，不定义执行依赖，也不等于一次业务任务。

Conversation 是一个对话框。习惯上称用户的主会话为 **User conv**，Operator 组织的工作会话为
**Operator conv**。这两个名称表达组织归属，不是两套模型，也不限制其中的消息发送者。
内部协作进入 Operator conv，用户问题、反问卡片与最终回答可以进入 User conv，避免复杂过程干扰主对话。

User conv 不绑定固定执行者，每次发言可以选择不同 Operator/Harness。定向发给 A 的消息只唤醒 A；
其他参与者可以主动 Read 历史，自行决定是否参与，而不是被隐式广播调度。
工作会话由 server 在主会话接收定向消息时按父会话与组织 Actor 分配或复用，跨多次发言
持续存在；不绑定一次页面交付。server 将其 ID 投影到主 Conv CRD 的对应参与者，Operator
直接读取关联，无需创建工作会话的 Verb。工作会话只组织消息，不拥有独立执行生命周期。

## Loop、Reconcile 与 Verb

```text
Loop = Resource(spec + status) + Reconcile
```

“Loop is a CRD” 描述 Operator 的业务编排：领域 CRD 持有状态，Reconcile 判断下一步，
Verb 将判断连接到实际协作能力。消息持久化和 Harness 调用记录由 Server 管理，不要求所有
协作状态都建模为 CRD。Verb 的 Effect 分为 read 与 write，不增加通用持久 Effect 引擎。

Reconcile 是可重复调度的执行入口，不是“一条消息执行一次”的回调。Operator 使用原生
controller-runtime 管理 Watch 与调度，runtime 提供接入辅助和协作 SDK，不另建调度器。

Operator 自己决定何时接收补充发言、如何组织工作，以及是否需要领域 CRD。
runtime 不把普通发言自动解释成 steer/followup，也不替 Operator 定义业务任务。
具体业务策略属于 Operator，不能反过来成为所有参与者必须遵循的交互回合。

## 控制信号与协作数据

Conv CRD 承载 Server 与 Operator 之间的控制信号和协调状态：参与者的过程会话关联、消息
水位与消费进度。Server 在消息提交 DB 后更新 Conv CRD，Operator 通过 Watch 被唤醒。通知
失败由 Server 重试；唤醒只提示可能有工作，不能把 Watch 事件数量或顺序当作消息记录。

消息正文、历史、Human 交互与 Harness 调用通过 Server API 读写。runtime 的 Poll/Commit
同样调用 API：Server 读取 DB 中面向参与者的消息并记录接收位置，按 Operator 的确认推进
CRD 消费游标。CRD 水位是唤醒提示，消息读取以 DB 为准；Read 只观察历史，Poll/Commit
改变消费位置，因此是 write Verb。具体消费保证见 [Conversation](../server/docs/conversation.md)。

实时输出通过 Server 的 SSE 接口交付，由 Redis Stream 提供增量：UI 订阅 Conv stream，
Operator 通过 Call 订阅 Run stream。DB 快照负责历史读取和缺口恢复，实时监听使用 Redis。
Operator 自己的领域 CRD 仍通过 Kubernetes Client 操作；这与调用 Server 的协作 Verb 分工明确。

## 交付与恢复

用户首次提交只创建真实的 user Message；Operator/Harness 回答、Ask、Confirm 在实际发起时
各自创建 Message，不预建空回答。一个执行循环可以接收多次发言，也可以多次发布阶段结果或回应。

提交返回真实消息及交付标识，页面以 Conv 独立订阅，以 Message 合并流式更新和恢复快照。
交付标识不对应通用 Task CRD 或 task 表，也不决定消息是新业务工作、补充信息还是确认答复。显式卡片回复给 typed Verb 返回值，
普通发言交给 Operator 判断，不自动解释为批准。

每条 Message 独立寻址、更新和持久化；Speak 可以一次说完，也可以逐步输出后 End。
End 只表示说完这条消息，不结束 Conv 或业务工作。页面流只聚合传输，其连接生命周期由页面管理，
不是 Operator 的完成动作。
连接断开不取消执行，任意 server 实例可以续接页面流；Redis 丢失时只能恢复已固化快照。

Server 是人、Operator、Harness 的协作平台；loop-runtime 是 Operator toolkit。
Server 内部 HarnessRunner 独立于 Operator 运行，持久接收调用、驱动 Adapter、保存可见输出并
承接进程故障后的重新挂接。harness_runs 是基础设施调用记录，不是 Operator 领域状态或 Agent
内部执行状态；resource_locks 为 Server 各后台组件提供通用租约。

恢复责任分层：

- 编排恢复依赖 Operator 持久化的 CRD 领域进度；Conv 游标不能恢复 Go 调用栈。
- Harness 恢复由 Adapter 和执行端保证；agentd 可承载持久执行，agentgo 是进程内 demo。
- Server 负责调用记录、输出落库和驱动者接管；聊天层负责消息快照、通知重试及流式续接。
- DB 保存合并后的 AgentUE 快照，Redis Stream append-only保存实时事件；二者的区别见持久化文档。

## 文档分工

Kernel 只定义跨功能稳定的 Actor 模型、协作主线与恢复责任。调用契约和领域机制分别由以下
文档拥有，不在 Kernel 展开参数、状态分支或示例 Operator 的策略。

| 文档 | 回答的问题 |
|---|---|
| [Harness](../server/docs/harness.md) | Server 如何接收、驱动和恢复 Harness 调用，Operator 如何获取最终结果？ |
| [Runtime](runtime.md) | Operator 开发者如何接入、组合 Verb，并承担哪些调用与恢复责任？ |
| [Conversation](../server/docs/conversation.md) | 持久消息如何定向通知、Poll、Commit，消费与重试保证到哪里？ |
| [持久化](../server/docs/persistence.md) | 可见事实存在哪里，User/Operator conv、消息身份和快照如何归属？ |
| [用户交互](../server/docs/ue.md) | 页面如何布局、呈现消息与交互卡片，如何流式交付、重连和收尾？ |

README 面向使用者介绍价值与最短使用路径；AGENTS.md 保留代码地图、关键约定及上述文档索引。

Actor 的内置 kind 为 user、operator、harness；Operator 可用 `operator/<operator-key>/<role>`
标识自己的参与者。自定义 kind/key 同样拥有发言身份和消费位置，不把领域角色注册进 Core。
例如 LongHorizon 三个角色共享工作会话，以 Run UID 区分作者实例，细节由 Operator 文档定义。
