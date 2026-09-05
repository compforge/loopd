# Operator 接入与 Harness 编排

Operator 通过 loop-runtime 读取会话、观察 Task、调用 Harness 并发布结果。领域逻辑只依赖这些
公共契约，server 的数据库模型和 HTTP 适配细节留在平台内部。

## 注册、唤醒与上下文

启动时通过 `Loop.Operator.Register` 注册可见信息；runtime 持续续租，server 将在线目标聚合
给 Chat UI。可直聊 Harness 使用 `Loop.Harness.Register`。租约边界见
[在线发现](../server/docs/registry.md)。

通过 `Loop.Task.Watch` 为自身目标绑定 controller-runtime Reconciler。通用 Task 当前携带
target 和 revision，名称就是 task_id；Reconciler 使用 `Loop.Task.Get` 读取最新 input、
response、Conversation 和有界 History。Task 先于聊天事务提交时应重试上下文查询。

简单 Operator 直接 Reconcile 通用 Task。复杂 Operator 可以创建自己的领域 CRD，在 spec/status
中保存领域目标、进度和完成条件；Query、完整 History 和高频流式事件不复制进通用 Task。
事件只提示事实可能变化，每次 Reconcile 都应重新读取权威状态。

Conversation 的历史不因切换 Operator 而复制或切断。`Chat.Conversation`、`Chat.History` 提供
显式读取入口；需要长期保存上下文水位时，由 Operator 的 Resource 记录相关 Message ID。

## Harness Call

`Loop.Harness.Prompt` 返回 Call handle，调用方可以继续处理其他变化、用 `call.Stream` 观察事件，
也可以在只剩结果未完成时调用 `call.Wait`。等待方式不改变外部执行身份，流式进展也不等于完成。

```go
call, err := r.Loop.Harness.Prompt(ctx, runtime.Prompt{
    TaskID:    task.ID,
    EffectKey: "route-query",
    Target:    "agentd",
    Text:      prompt,
    Tools:     tools,
})
```

同一 runtime 内，TaskID 与 EffectKey 相同的请求复用同一个 Call；参数冲突必须拒绝。该去重范围
是进程内，生产 Adapter 必须用稳定 IdempotencyKey 在重启后恢复同一次外部执行。AgentGo Adapter
用于进程内 demo，单 Harness 的持久执行恢复由 agentd 等外部实现承担。

Prompt 和 tools 由调用方选择，Adapter 转换协议与事件。Operator 领域逻辑不接触模型协议中的
assistant/system/tool 等角色，Harness provider 差异也不进入 Conversation 模型。

## 发布过程与回答

`Chat.Emit` 发布可见过程，`Chat.Complete` 固化回答并完成交付。内部 Harness 可以贡献详情和
工具状态，最终主回答由用户选中的 Operator 汇总。流式事件、最终快照和完整轨迹的边界见
[Task 交付](../server/docs/task-delivery.md) 与 [聊天持久化](../server/docs/persistence.md)。

Human 交互沿用一次问答的 Task identity。专门的 Ask/Confirm 能力尚未实现；不能把带 task_id
的 `Chat.Send` 当作提交反问答案，它当前用于续接已有问答。

## Router 示例

Router 根据 Query 和 Conversation History 判断复杂度：简单请求调用一个临时 Harness，复杂
请求拆成多个子问题，串行或并行执行后汇总。拆解、依赖关系和完成条件均由 Router 拥有。

当前临时 Harness 不单独注册为直聊目标。接入已注册 Harness 候选后，选择、转发和分发策略仍由
Operator 定义；loopd 的通用内核不固定角色分工、DAG 或某一种 Agent Team。
