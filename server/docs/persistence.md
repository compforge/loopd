# loop-server 持久化

本文定义页面可见事实的存储归属与身份：数据库保存 Conversation、Message 和 Operator/Harness
在线注册。同一份 Message 同时支持页面历史与参与者消费，不另建一份业务消息 queue 表。
消费协议由 [Conversation](conversation.md) 定义，跨存储的责任分层见 [Kernel](../../docs/kernel.md)。

数据库差异由 repo 的 GORM Dialector 封装，使用 DATABASE_DRIVER 与 DATABASE_DSN 配置。
Quick Start 默认临时 SQLite，持久部署由使用方提供外部 MySQL。Redis 是事件交付桥，不是数据库
或业务执行状态的替代品。loopd 不管理外置数据库实例。

## 会话归属

Conversation 是一个对话框，actor_kind/actor_key 表达组织归属，不限制谁可以在其中发言。

- **User conv**：用户组织的主会话，parent_id 为空，供多个参与者持续交流。
- **Operator conv**：Operator 的内部协作会话，parent_id 指向 User conv，归属于指定 Operator。
  多个 Harness 和其他 Actor 可以在其中发言，避免过程淹没主会话。

两者是习惯用语，不是两套模型。Harness 组织的工作会话使用自身 actor_kind，不伪装成 Operator。
Conv.Workspace 按 parent_id + actor_kind + actor_key 创建或复用内部会话，同一 Actor 跨多次输入
共享详情。Conversation 不保存 task_id，也不依赖某条回答先存在。

左侧导航只列主会话。选中消息后，按其会话与 Actor 查找内部会话；用户消息使用收件 Actor，
其他消息使用发言 Actor。右侧显示该内部会话的消息，而不是按一次输入截出一份执行日志。

## Message 是发言事实

Message 用 kind/actor_key 表达发送者，target_kind/target_key 表达收件者。收件者均为空字符串
表示对会话发言。reply_to_id 指向同一会话的具体消息，不表达任务身份或执行顺序。

人可以连续追加消息，Operator 可以分多次回应；没有一问一答约束，也不预建空回答。
Speak 的稳定 Key 以 Conversation + Actor 为范围，存储层生成全局唯一 output_key。
同身份重试返回已有消息，新发言使用新 key；TaskID 不参与发言幂等身份。

消息保存 AgentUE semantic model JSON，包含 version、biz、meta 与 blocks。文字、工具展示、
文件等可以共存一条消息；delta 不另建 Message。完整 prompt、工具原始输入输出、重试与成本
属于执行轨迹，只有页面需要展示的部分进入 Message。

revision 表示可见快照版本，流式输出对应 AgentUE seq，Human 状态转换在事务内递增。
每条消息分别更新，不能因为 block ID 相同就跨消息合并。持久化与 replay 见
[页面交付](ue.md#页面交付)。

task_id 仅关联 UI／Redis 交付，可以为空。purpose=input 的消息是开启该交付的真实用户发言，
其 delivery_state/completion 保存 UI 关闭意图；这些字段不控制 Actor 是否还可以发言。
output、human_request、human_reply 分别表达普通输出、交互问题和卡片答复，不指定唯一主回答。

## Human 状态

问题与答复继续以 Message 为唯一事实来源，不新增 Interaction 表或 CRD。
受控 ask/confirm block 保存状态和 deadline，meta 保存 EffectKey 与请求指纹。
human_due_at、wake_pending 是维护循环使用的投影，不另存问题正文。

问题以 Conv + Actor + EffectKey 复用，期限不因重试重置。有效答复原子创建 user Message，
收口问题并留下定向通知；重复相同答复幂等，矛盾或迟到答复拒绝。超时不伪造 user Message。

reply_to_id 是答复关联的唯一依据，不能用最近消息、相邻位置或 task_id 猜测。
问题生命周期独立于 UI delivery；关闭页面流不取消问题，普通发言不自动表示批准。
类型化契约见 [Runtime](../../docs/runtime.md)。

## 时间与分页

表主键使用 UUIDv7，消息按 id 排序并分页，不维护额外 sequence。
UUIDv7 的时间顺序不是多节点数据库的全局提交顺序；当前采用人类输入通常有先后的假设，
严格消费顺序的限制见 [Conversation](conversation.md)。

created_at 与 updated_at 表达首次到最后一次可见活动。Harness 事件携带时间戳，
完成投影不把每条消息的结束时间改成整个页面流的完成时间，重试不缩短活动区间。

时间区间如何用于并行展示见 [消息呈现](ue.md#消息呈现)，不由存储层规定页面布局。
