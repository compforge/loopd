# loopd Kernel

loopd 是 “Loop is a CRD” 在编排层的实现，也是 Human、Harness 与 Operator 的协作平台。
独立参与者通过持久化消息协作：各自运行，自己决定何时接收消息、何时回应，处理到安全位置再确认消费。
Conversation 是共享交流空间，不是由一次请求驱动、等待一个答案后结束的固定工作流。

## 协作模型

Human 可以在 Operator 工作期间持续发言；Operator 可以先回应一部分，再继续工作和发言。
双方不互相占有生命周期，也不必轮流执行。Operator 通过 Poll 接收输入，通过 Speak 回应，
在结果或领域进度足以支持安全恢复后 Commit；何时做这些事，由各 Operator 的业务决定。

这套参与者模型也容纳 Harness。角色描述身份，不表示谁是主、谁是辅；但统一身份不等于统一
执行协议：Operator 使用 Poll/Commit，Harness 的接入和执行恢复仍由 Adapter 契约负责。

协作所需的状态各有归属：

| 载体 | 责任 | 不承担 |
|---|---|---|
| DB | 保存 Conversation 与 Message，作为可查询、可重复消费的消息记录 | Operator 领域进度、完整执行轨迹 |
| Conv CRD | 保存参与者消费进度与定向信号，通过 Watch 触发 Reconcile | 消息正文、业务工作完成判定 |
| Redis | 传递页面增量，支持跨 server 实例重连与 replay | 参与者消费进度、业务执行恢复 |

可以把 DB 理解成协作 queue，但消费不会删除消息，也不争抢一个全局消费位置；参与者各自
维护消费进度，历史仍可 Read。Poll 表示收到，Commit 表示可安全越过该消费前缀，不表示整个
会话或 Operator 的工作结束。

这里参考 Kafka 的 Producer 与多 Consumer 模型：参与者发言时是 Producer，接收消息时是
Consumer，同一参与者可以兼具两种职责。不同 Actor 独立消费面向自己的消息、独立提交进度，
更接近不同 consumer group 各自消费，而不是同一 group 内竞争分配消息。loopd 借用这种
生产与消费解耦的协作语义，不实现 Kafka 的分区、消费者组协调协议，也不宣称具备其全部日志保证。

## 定位与边界

- loop-server 拥有页面可见 Conversation、Message、在线注册和消息交付。
- loop-runtime 是嵌入 Operator 的 Go toolkit，复用 controller-runtime，提供协作 Verb。
- Operator 决定业务含义、消息如何组成工作、何时接收补充信息及何时完成。
- Harness 通过 Adapter 提供智能执行；执行状态与恢复属于 Harness。
- AgentLedger 承载完整执行轨迹，不替代可见聊天记录。

Operator 不依赖 server 的私有 model/repo，不直接操作聊天数据库或 Redis。
server 不导入 Operator 领域 CRD，也不执行 Harness Adapter。

## 参与者与会话

公开角色只有 `user`、`operator`、`harness`。Message 的发送者和收件者各有 kind/key，
回复引用表达“回应哪条消息”，不定义执行依赖，也不等于一次业务任务。

Conversation 是一个对话框。习惯上称用户的主会话为 **User conv**，Operator 组织的工作会话为
**Operator conv**。这两个名称表达组织归属，不是两套模型，也不限制其中的消息发送者。
内部协作进入 Operator conv，用户问题、反问卡片与最终回答可以进入 User conv，避免复杂过程干扰主对话。

User conv 不绑定固定执行者，每次发言可以选择不同 Operator/Harness。定向发给 A 的消息只唤醒 A；
其他参与者可以主动 Read 历史，自行决定是否参与，而不是被隐式广播调度。
工作会话按父会话与组织 Actor 复用，跨多次发言持续存在；不绑定一次页面交付。

## Loop、Reconcile 与 Verb

```text
Loop = Resource(spec + status) + Reconcile
```

CRD 持有状态，Reconcile 判断下一步，Verb 将判断连接到实际协作能力。仅对资源做 CRUD 不足以
完成编排；runtime 联合 server 提供读取数据、调用 Harness、Ask/Confirm、发布与持久化流式输出、
间接送达页面等机制。Verb 的 Effect 分为 read 与 write，不增加通用持久 Effect 引擎。

Conv CRD 是 server 与 Operator 之间的协作边界。Reconcile 是可重复调度的执行入口，
不是“一条消息执行一次”的回调；唤醒只提示可能有工作，Poll 仍以 DB 的消息记录为准。
Read 只观察历史；Poll/Commit 改变消费位置，因此是 write Verb。

Operator 自己决定何时接收补充发言、如何组织工作，以及是否需要领域 CRD。
runtime 不把普通发言自动解释成 steer/followup，也不替 Operator 定义业务任务。
具体业务策略属于 Operator，不能反过来成为所有参与者必须遵循的交互回合。

## 交付与恢复

用户首次提交只创建真实的 user Message；Operator/Harness 回答、Ask、Confirm 在实际发起时
各自创建 Message，不预建空回答。一个执行循环可以接收多次发言，也可以多次发布阶段结果或回应。

`task_id` 是 UI/Redis 流的身份：不带它提交新发言，带它 replay。它不对应通用 Task CRD 或 task 表，
也不决定消息是新业务工作、补充信息还是确认答复。显式卡片回复给 typed Verb 返回值，
普通发言交给 Operator 判断，不自动解释为批准。

每条 Message 独立寻址、更新和持久化；页面流只聚合传输。Message 的 end 与页面流 end 相互独立。
连接断开不取消执行，任意 server 实例可以续接页面流；Redis 丢失时只能恢复已固化快照。

恢复责任分层：

- 编排恢复依赖 Operator 持久化的 CRD 领域进度；Conv 游标不能恢复 Go 调用栈。
- Harness 恢复由 Adapter 和执行端保证；agentd 可承载持久执行，agentgo 是进程内 demo。
- 聊天层负责消息快照、通知重试、流式续接与交付收尾，不接管以上执行恢复。

## 文档分工

Kernel 只定义跨功能稳定的参与者模型、协作主线与恢复责任。调用契约和领域机制分别由以下
文档拥有，不在 Kernel 展开参数、状态分支或示例 Operator 的策略。

| 文档 | 回答的问题 |
|---|---|
| [Runtime](runtime.md) | Operator 开发者如何接入、组合 Verb，并承担哪些调用与恢复责任？ |
| [Conversation](../server/docs/conversation.md) | 持久消息如何定向通知、Poll、Commit，消费与重试保证到哪里？ |
| [持久化](../server/docs/persistence.md) | 可见事实存在哪里，User/Operator conv、消息身份和快照如何归属？ |
| [用户交互](../server/docs/ue.md) | 页面如何布局、呈现消息与交互卡片，如何流式交付、重连和收尾？ |

README 面向使用者介绍价值与最短使用路径；AGENTS.md 保留代码地图、关键约定及上述文档索引。
