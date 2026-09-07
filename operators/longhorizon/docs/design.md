# LongHorizon 领域设计

LongHorizon 实现长期 CLI 工作，不向 server 增加领域表。它遵循 [独立参与者模型](../../../docs/kernel.md)：
DB 保存可见消息，Conv 保存消费位置，Redis 只服务页面交付，Harness Adapter 拥有执行恢复。
Operator 不接触 task_id 或页面交付生命周期；Run 身份、期限、工作目录和 Harness 幂等键
均由业务定义。历史与具体消息引用通过分页 Read 获取，不恢复 Context Verb。

## 资源和并发边界

```text
Conv (loopd.compforge.io)
└─ Run (longhorizon.loopd.compforge.io)
   ├─ Execution — plan + contractVersion
   └─ Audit — execution UID + contractVersion + executionMessageID
```

Conv ingress 只接收新一轮工作的首条输入并创建 Run。三个业务 Reconciler 分别写 Run、Execution、
Audit 的 status，使用 resourceVersion 乐观锁，spec 不可变。Audit 直接归 Run 所有，执行依赖用
精确引用表达；轮次放在 Run 的最近 50 条摘要里，Conditions 只表示整体状态，不建立 Round CRD。

相同 Conv/Actor 的 intake 只有一个 owner：Ingress 使用权威 Reader 检查活跃 Run，已有 Run 时
由 Manager 接收补充输入；Run 记录 FinishedAt 后才允许新 Run。Controller 使用 leader election，
不能以乐观锁替代执行互斥。每次派发前重新检查自身和祖先 UID、删除标记及 Run 期限。

Manager 的决策只有 cli、ask、blocked、done。Executor 提供 Bash/Read/Write/Edit/Ls/Glob/Grep；
Auditor 只有 Read/Ls/Glob/Grep，核对真实工件、原始目标、人工补充和当前契约。done 必须有当前
契约版本的 clean + complete 审计和非空证据；模型给出的结论仍需部署后的实际任务验证。

## 消费检查点和补充消息

LongHorizon Operator 当前暂不自动识别“新任务”还是“老任务继续”。同一 User conv 发给
LongHorizon 的后续输入，在当前 Run 未收尾时视为补充，由 Manager 在业务安全边界接收；
不会因为消息换了主题就另建 Run。用户希望独立开展工作时，可以新建 User conv。

Run 已成功、停止或失败，并且最终总结持久化、记录 FinishedAt 后，下一条未消费输入可以创建
新 Run。新 Run 复用 Operator conv 并读取稳定会话历史，但拥有独立的领域状态、期限和工作目录。
这由 Run 生命周期决定，不是对消息语义的新旧任务分类；已有 Run 的 TTL 回收不阻塞新 Run。

Run 名称取首条输入 Message ID，归属 Conv UID。初始化 status 保存已接受输入引用及 InputThrough，
下一次 Reconcile 才 Commit；失败时先重试 Commit，再进行任何新步骤。Commit 表示已保存可恢复
检查点，不表示目标完成。Run 活跃期间不因为 Committed 前进而创建第二个 Run。

执行和审计过程中不改变当前 prompt。在下一轮的 Receiving 阶段，Manager 从 InputThrough 后 Poll，
将同一用户的补充要求保存为 Guidance 和引用，提升契约版本、清除旧审计，再进入 Planning。
当前边界最多拉取 32 条，单 Run 最多接收 100 条输入，Guidance 限 16000 字节；越界输入保留在
未提交范围，待当前 Run 结束后开启下一 Run。其他普通输出可确认观察，不能作为批准。

规划过程中到达的消息留给下一个边界。最终总结只对应已接收前缀；总结时到达的新输入由下一 Run
接收，不通过无限排空阻止完成。新的 Run 保留最多 20 条稳定历史引用，读取上下文限制 16000 字节。

## 可见消息和恢复

三种作者为 `operator/longhorizon/manager`、`operator/longhorizon/executor`、
`operator/longhorizon/auditor`，key 均为 Run UID。角色共享 `operator / longhorizon` 的过程会话，Ingress 从主 Conv 的参与者
`conversationID` 读取 server 已分配的 ID，并持久化为 Run.Spec.WorkspaceID；不调用创建会话的 Verb。
右侧按完整 kind/key 区分列，标题显示轮次；主会话的角色消息也定位到这个共享 Workspace。
消息时间区间可以并行，因果引用依赖 reply_to_id 或 CRD 内精确引用。

每轮每个角色调用对应一条 Server 创建的 Message。步骤幂等身份为
`<runUID>/round/<n>/<role>`；Prompt 指定角色 Actor 与展示 Meta，Server 保存过程 blocks，
并将 Adapter 识别出的最终输出存为 result block，与终态原子提交。Operator 读取权威结果再更新
CRD status，不接手流式 Emit/End，也不把部分 token 当成可恢复报告。

CRD status 写入丢失或 Operator 重启后，相同 key 返回同一个调用和已保存结果。主会话最终总结
仍由 Operator 用默认 Speak 一次说完。Manager 消费结果后先持久化推进状态，再删除已消费轮次的
Execution/Audit；长期内容留在 Message，CRD 只保存有界控制状态和引用。

未完成调用由 Server 接管驱动，Harness 执行恢复及外部副作用去重由 Adapter 与执行端保证。
AgentGo demo 不支持跨进程恢复，接管明确返回 unknown，不自动重复执行。

## Human 和生命周期

Ask 可提供单选、单选加自由文本或纯自由文本；Confirm 提供 Continue / Finish here。问题作者为
Manager，收件人为原用户，reply_to_id 关联输入，答复通过精确问题 ID 返回类型化结果。三次连续
失败或预算耗尽请求确认；同意可重试或追加 25 轮，但不延长 Run deadline。

timeout、dismissed、declined 都是正常结果，默认停止 Run 并发布总结。成功 Ask 补充 Guidance，
使旧审计失效。普通用户消息不替代卡片结果。Run 结束和关闭 UI 流不取消未答问题；问题依赖自己的
deadline，到期后由 server 收口，后来的答复也不会复活已结束 Run。

Run deadline 和 retention TTL 都属于 Operator。最终总结通过主 Conv 的幂等 Speak 持久化后，记录
FinishedAt；到期删除 Run，Kubernetes 后台 GC 删除剩余子对象。已消费子对象可更早删除，不给 Conv
或 Run 添加 finalizer。Conv、消费游标与 Message 保留，页面持续订阅会话并发现结果。
Operator 不关闭页面流；结束某条 Message 不结束 Run、Human 或其他 Actor 的工作。

删除中、缺失或 UID 被替换的资源不再派发；已发起 Harness 的实际取消或恢复仍由 Adapter 负责。
工作区文件保留与 Kubernetes GC 分离；容器工具没有跨 Run 的强安全隔离。

## 验证契约

```sh
go test ./...
go test -race ./operators/longhorizon/... ./runtime/... ./pkg/harness/agentgo/...
cd web && npm run check
```

控制器测试覆盖每次角色调用只发布一条消息、流式输出和工具保留、过期审计拒绝 done、三角色纠偏、
报告持久失败和重启补写、补充输入检查点与 Commit
失败、页面流独立、Human 正常兜底、Conv 删除或替换、Run 期限和回收以及有界轮次。测试用 fake
Kubernetes 和公共 HTTP fixture；API 测试另外验证自定义角色发问、定向回复及独立 Poll/Commit。
生成 CRD 以 Kubernetes 自身校验器检查 Schema/CEL 成本。上述检查不替代真实集群 Watch、RBAC、GC
和真实模型执行的部署后验证。

## Server Harness Run

角色调用通过 loop-runtime 向 Server 提交，Server HarnessRunner 驱动 Adapter，并原子持久化
result block 和终态。每个角色调用拥有独立 Message；Operator 直接消费结果，不再绑定 Output
writer 或接手流式 End。业务 Run/Execution/Audit 仍由 Operator 管理，Server harness_runs 仅记录
基础设施调用。业务 CRD 状态写入丢失后，相同调用 key 从 Server 恢复已有结果。
