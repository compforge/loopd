# Conversation 消息消费

Conversation 是参与者共享的交流空间，Message 是可见内容的事实来源。Conv CRD 保存参与者的
唤醒信号与消费位置，不保存消息正文或 Operator 的领域状态。

本文拥有持久消息的通知、拉取与提交协议。Actor 模型见 [Kernel](../../docs/kernel.md)，
Verb 的调用方式见 [Runtime](../../docs/runtime.md)；这里不规定 Operator 何时处理补充输入。

## DB 作为协作 queue

参与者通过保留的 Message 协作，不直接等待对方调用返回。各 Actor 在同一 Conv 中有独立的
消费位置：A Commit 不会替 B 确认，也不会删除历史。Read 是查看共享记录，Poll 是领取自己的
待消费输入；两者不能混用来推断“已经处理完”。

这不是严格只追加的事件日志：消息内容仍可以按 revision 更新，Poll 的位置按 Message ID
推进，不把每次流式增量当作新输入。Redis 的流位置服务页面重连，与 Actor 的消费位置无关。

## 定向消息与共享历史

发送者由 kind/key 表达，收件者由 target_kind/target_key 表达。收件者为空字符串表示广播给会话
中的参与者，不表示所有已注册 Operator 自动加入。发给 A 的消息只唤醒 A；B 可以主动 Read
共享历史，自行决定是否参与。参与者不消费自己的广播，避免输出反过来驱动自身。

Read 是 read Verb，分页返回共享历史；历史范围与执行上下文由 Operator 自行组织，不推断问答配对。
Speak 是 write Verb，在指定会话中创建或幂等复用 Actor 的一条消息；可以另发消息，也可以按消息
身份继续更新流式内容。Speak 不承诺对方已经消费，更不表示业务完成。

流式 Speak 在 End 后才进入收件 Actor 的消费前缀，页面和 Read 则可以观察实时内容。
Poll 遇到尚未 End 的收件消息即停止，不能越过它提交后面的消息；一条较早的消息结束时仍会
通知收件 Actor，即使已有更大的 EndOffset。写入者须完成或恢复自己的输出，runtime 不凭时间
猜测其结束。希望先交付阶段结果时，可用独立的一次性 Speak，而不是让接收者消费半条消息。

## Poll 与 Commit

消息消费参考 [Kafka Consumer](https://kafka.apache.org/41/javadoc/org/apache/kafka/clients/consumer/KafkaConsumer.html)
的日志、拉取与提交语义：DB 是保留的消息日志，CRD 保存各 Actor 在 Conv 中的消费位置。
这是类比，不意味着接入 Kafka、消息出队删除或提供 Kafka 的分区与消费者组协议。

| 位置 | 含义 |
|---|---|
| EndOffset | 当前参与者最新的消息通知位置，仅用于唤醒 |
| Position | 服务端记录的最高已拉取位置，不代表已安全处理 |
| Committed | 调用者确认可安全恢复的位置 |

位置值是 UUIDv7 Message ID，表示包含该消息的边界；不是 Kafka 数字 offset 的“下一条”约定。

1. server 保存可消费 Message（流式输出为 End）时同事务记录待通知标记，提交后更新 CRD 的 EndOffset；失败由后台重试。
2. controller-runtime Watch 将参与者信号映射到 Reconcile；Watch 是 Controller 配置，不是 Verb。
3. Poll 默认从 Committed 后读取定向或广播消息，并记录 Position，不自动 Commit。
4. 同一次执行继续拉取时显式传上次结果 Position 作为 After；丢失响应时用相同 After 重试。
5. Operator 在持久化领域检查点、完成处理或明确记录失败结果后，Commit 连续安全前缀。

Poll 查询以数据库为准，不把 EndOffset 当作上限。Commit 单调推进，不能超过已记录的 Position；
调用者负责保证前缀内没有尚未安全处理的消息。它不等于结束业务，也不关闭页面流。

## 恢复与调度

进程重启或 Poll 响应丢失后，不传 After 即从 Committed 恢复未提交消息，提供至少一次消费的基础。
这不能保证外部动作 exactly-once：Operator 仍需稳定动作身份和必要的领域检查点。

EndOffset 保持单调；Actor 专属的消息版本通知也能唤醒较早流式消息结束后的消费。
Predicate 不因 Position 更新而触发空转；启动时未提交的消息、自己的新通知或仍有积压的 Commit
会触发调谐。其他参与者的变化不唤醒当前 Actor。相同 Actor/Conv 的消费循环应由 Operator 保证
单一 owner；Kubernetes 资源版本解决状态更新冲突，不替代多副本执行互斥。

UUIDv7 使用当前人类消息通常先后产生的时间有序假设，不宣称多节点数据库的全局提交顺序。
多个写入节点、长事务或时钟偏移可能让较小 ID 较晚可见；需要严格日志顺序时应另行设计排序保证。

## Human 与业务边界

Ask/Confirm 的卡片回复有精确 reply_to_id 和类型化结果；普通发言不会被自动解释为批准。
卡片回复也是定向 Message，Operator 可以 Poll 感知；超时不伪造消息，由 handle 或 deadline 调度感知。

何时 Poll、补充消息是否合并进工作、是否需要领域 CRD，由 Operator 决定。编排恢复靠领域 CRD，
Harness 恢复靠 Adapter；Conv 消费位置不恢复 Go 调用栈。

消息和会话的存储归属见 [持久化](persistence.md)，页面交互与续接见 [UE](ue.md)。
Operator 只使用 runtime，不导入 server 的 repo/model，也不直接访问聊天数据库或 Redis。
当前 Operator API 使用可信部署边界，不构成完整多租户身份认证或消息可见性 ACL。
