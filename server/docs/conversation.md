# Conversation 消息接收

Conversation 是参与者共享的交流空间。Message 是可见内容的事实来源，Conv CRD 保存参与者的
唤醒信号与接收游标；Operator 的业务状态不进入这些字段。

Chat 的 `task_id` 用于页面流、Redis 寻址与 replay，不应解释为 Operator 的业务任务身份。
一次 Chat 的流结束，不意味着整个 Conversation 或 Operator 的业务工作结束。

## 定向消息与共享历史

Message 的发送者由 `kind/actor_key` 表达，收件者由 `target_kind/target_key` 表达。两列均为空字符串
表示显式广播，不表示所有已注册 Operator 自动加入会话。发送给 A 的消息只唤醒 A；B 可以主动
读取共享历史，自行决定是否参与，但不会因这条定向消息被隐式调度。

Read 返回会话历史，不改变接收状态。Listen 是 write Verb：从数据库读取发给当前参与者或广播的
新消息，并推进该参与者在 CRD 中的游标。参与者不接收自己的广播，以免输出反过来驱动自身。

历史数据增加收件者列时，缺少收件者的旧行保留 SQL NULL，不推断为广播；新消息会明确写入收件者
或两个空字符串。历史记录仍可通过 Read 查看。字段和索引由现有 GORM 迁移入口维护。

## 接收流程

1. server 在保存 Message 的同一事务里记录待通知标记，提交后再更新对应参与者的 CRD 唤醒信号。
2. CRD 更新成功后清除待通知标记；失败则保留，由任一 server 实例重试。重试通知不新建 Message。
3. Operator 的 Watch 接收自身信号变化。不同参与者的信号分别保存，避免 Kubernetes 合并更新时
   覆盖另一参与者尚未处理的通知。
4. Listen 从 CRD 取当前游标，再按 Message ID 升序查询 DB，返回有界批次。没有结果不推进游标；
   有结果则保存最后一条已接收消息的 ID。

CRD 中的最新消息 ID 只用于唤醒，不是 Listen 查询的上限，也不能替代查询 DB。Operator 可以在
需要时主动调用 Listen，而不必等到唤醒信号追上数据库。

## 恢复与调用边界

游标依赖 UUIDv7 的时间顺序，采用当前人类消息通常有先后的 v1 假设，不宣称多写入节点的全局提交顺序。
并发接收用 Kubernetes 资源版本处理冲突，冲突后重新读取游标与消息，不合并旧快照覆盖其他参与者。

接收不是业务完成，也不是业务检查点。游标更新后若 HTTP 响应丢失，调用者可能没有拿到该批消息；
Read 保留找回事实的途径，但 Listen 不提供 exactly-once 执行保证。Operator 决定何时接收、
何时持久化领域进度，以及新发言属于补充信息还是另一项工作。

Ask/Confirm 的卡片答复具有精确的消息引用和类型化返回值；普通发言不会被自动当作确认批准。
Human 答复本身是定向 Message，其可见状态和通知仍由各自用例维护。

Operator 通过 runtime 使用这些能力，不导入 server 的 model/repo，也不直接操作聊天数据库或 Redis。
当前接收接口沿用 Operator API 的可信部署边界，不构成多租户身份认证或消息可见性 ACL。
