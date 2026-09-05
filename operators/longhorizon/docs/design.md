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
`operator/longhorizon/auditor`，key 均为 Run UID。角色共享 `operator / longhorizon` 的 Workspace，
右侧按完整 kind/key 区分列，标题显示轮次；主会话的角色消息也定位到这个共享 Workspace。
消息时间区间可以并行，因果引用依赖 reply_to_id 或 CRD 内精确引用。

步骤身份为 `<runUID>/round/<n>/<role>`，Call 和 report 使用不同后缀。Harness.Prompt 的 Actor
控制可见过程作者，Timeout 交给 Adapter。角色报告通过 Speak(Stream=true) 建立消息句柄，
完整 report block 经 handle.Emit 持久化，再 End 后才更新 CRD status。重试 Speak 返回既有快照；
报告已存在但 End 未确认时，只重试 End，不重新启动 Harness。已结束报告可直接补写 status。
序号分配、瞬时重试及未确认更新的顺序由句柄负责。主会话最终总结用默认 Speak 一次说完。


Manager 消费报告后先持久化推进状态，再删除已消费轮次的 Execution/Audit。长期事实留在 Message，
CRD 只保存有界控制状态和引用。尚未得到报告的执行恢复、外部副作用去重完全归 Adapter；AgentGo
示例只在进程内复用 Call，重启可能重新执行未完成步骤，不承诺恰好一次。

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
go test -race ./operators/longhorizon/... ./runtime/... ./harness/agentgo/...
cd web && npm run check
```

控制器测试覆盖三角色纠偏、过期审计拒绝 done、报告持久失败和重启补写、补充输入检查点与 Commit
失败、页面流独立、Human 正常兜底、Conv 删除或替换、Run 期限和回收以及有界轮次。测试用 fake
Kubernetes 和公共 HTTP fixture；API 测试另外验证自定义角色发问、定向回复及独立 Poll/Commit。
生成 CRD 以 Kubernetes 自身校验器检查 Schema/CEL 成本。上述检查不替代真实集群 Watch、RBAC、GC
和真实模型执行的部署后验证。
