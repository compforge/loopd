# loopd Kernel

loopd 是 “Loop is a CRD” 在编排层的实现，也是 Human、Harness 与 Operator 的协作平台。
参与者在持久 Conversation 中发言；server 保存 Message，通过 Conv CRD 唤醒收件者；
Operator 用 runtime Verb 读取、执行、协作并发布结果。

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

Conv CRD 是 server 与参与者之间的协作边界，只保存定向唤醒信号和接收游标，不复制消息正文。
Poll 是 write Verb：拉取发给参与者的消息并记录 Position；Commit 确认连续安全消费前缀。
Read/Context 读取消息与历史，不改变消费位置；Speak 发言，不暗示工作完成。
最新消息 ID 只是触发信号，Poll 仍从数据库查询。接收不是业务完成，不是领域 checkpoint。

Operator 自己决定何时 Poll、如何处理补充发言，以及是否需要自己的领域 CRD。
Router 示例在同一次执行中等当前批 Harness 完成，再 Poll 并重新 plan；不强制一条消息对应
一个业务任务，也不要求额外 Work CRD。LongHorizon 的角色和领域状态由其 Operator 自行定义。

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

## 领域设计入口

- [Runtime](runtime.md)：Operator Verb、注册、Poll、Harness 与 Human 协作。
- [Conversation](../server/docs/conversation.md)：定向接收、游标与恢复边界。
- [聊天持久化](../server/docs/persistence.md)：可见记录与处理详情。
- [页面交付](../server/docs/task-delivery.md)：提交、流式续接与完成重试。
